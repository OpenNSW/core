package storage

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	corestorage "github.com/OpenNSW/core/storage"

	"github.com/OpenNSW/nsw-srilanka/internal/storage/filetoken"
)

// TypeProxy is the storage type (storage.type) that serves storage from
// another service instead of a backend of this deployment's own. Every other
// type is a core/storage backend type.
const TypeProxy = "proxy"

// KeyPlaceholder marks where the storage key goes in ProxyConfig's
// DownloadPath and DeletePath.
const KeyPlaceholder = "{key}"

// DefaultTokenTTLSeconds is how long a file token a user reads with lasts
// when storage.tokenTTLSeconds is unset: one hour.
const DefaultTokenTTLSeconds = 3600

// Config is the storage configuration. Type (storage.type) selects either
// TypeProxy, which uses Proxy, or a core/storage backend, which uses the
// embedded core/storage settings. Those are inlined, so in config.yaml they sit
// beside proxy under the same storage section.
type Config struct {
	corestorage.Config `yaml:",inline"`
	// AllowedUploadTypes are the MIME types clients may upload; any other
	// type is refused with 415. Required for the local and s3 backends; not
	// read in proxy mode, where the owning service applies its own.
	AllowedUploadTypes []string `yaml:"allowedUploadTypes"`
	// AllowedInternalUploadTypes are the media types a machine client may
	// upload on the internal upload route beyond AllowedUploadTypes, by the
	// client's name in the global catalog's clients. Optional, and not read in
	// proxy mode. Unlike AllowedUploadTypes it may list a type InlineTypes opens
	// in the browser, such as text/html for a document an agency generates: the
	// route is open only to machine clients, and only the clients named here can
	// store such a type.
	AllowedInternalUploadTypes map[string][]string `yaml:"allowedInternalUploadTypes"`
	// UploadTypeExtensions lists, per media type, the extensions an uploaded
	// file's name may end in, in order of preference: a name ending in another
	// is refused with 400, and a name without one is given the first. The
	// stored file keeps the extension, and a download is named by it. It must
	// list every type AllowedUploadTypes and AllowedInternalUploadTypes do.
	// Required for the local and s3 backends; not read in proxy mode, where the
	// owning service applies its own.
	UploadTypeExtensions map[string][]string `yaml:"uploadTypeExtensions"`
	// InlineTypes are the media types a download opens in the browser; every
	// other file is saved. Required for the local and s3 backends; not read in
	// proxy mode, where the owning service applies its own. A type a browser
	// interprets as a document, such as text/html, may be listed only when
	// clients cannot upload it: it is for what this service stores itself.
	InlineTypes []string `yaml:"inlineTypes"`
	// MaxUploadBytes is the largest file, in bytes, clients may upload.
	// Required for the local and s3 backends; not read in proxy mode.
	MaxUploadBytes int64 `yaml:"maxUploadBytes"`
	// Proxy is used when Type is TypeProxy: files are served from another
	// service that owns them.
	Proxy ProxyConfig `yaml:"proxy"`
	// TokenKeyset holds the keys the file tokens callers hold in place of
	// stored values are sealed with: comma-separated "kid:key" pairs, newest
	// first, each key 32 random bytes in base64. Required in every mode, and
	// different for every deployment. Keep a key for as long as the tokens it
	// sealed must open; those given to OGAs never expire.
	TokenKeyset string `yaml:"tokenKeyset"`
	// TokenTTLSeconds is how long a file token a user reads with lasts, in
	// seconds: the tokens a task's view gives out. Optional:
	// DefaultTokenTTLSeconds when unset. A user's upload tokens, which can only
	// be submitted, and machine clients' tokens never expire.
	TokenTTLSeconds int `yaml:"tokenTTLSeconds"`
}

// IsProxy reports whether Type selects proxy mode.
func (c Config) IsProxy() bool {
	return strings.TrimSpace(c.Type) == TypeProxy
}

// TokenTTL returns how long a file token a user reads with lasts.
func (c Config) TokenTTL() time.Duration {
	if c.TokenTTLSeconds == 0 {
		return DefaultTokenTTLSeconds * time.Second
	}
	return time.Duration(c.TokenTTLSeconds) * time.Second
}

// Validate checks the file token settings, which every mode needs, and then
// the configuration Type selects: the proxy settings in proxy mode, otherwise
// the core/storage backend's — which would reject "proxy" as an unknown
// backend type. The local content routes always sit under RoutePrefix, so a
// storage.local.routePrefix that says otherwise is an error rather than
// silently ignored.
func (c Config) Validate() error {
	if err := c.validateTokens(); err != nil {
		return err
	}
	if c.IsProxy() {
		return c.Proxy.Validate()
	}
	if err := c.validateBackend(); err != nil {
		return err
	}
	return c.Config.Validate()
}

func (c Config) validateTokens() error {
	if strings.TrimSpace(c.TokenKeyset) == "" {
		return fmt.Errorf("storage.tokenKeyset is required: comma-separated kid:key pairs, each key 32 random bytes in base64")
	}
	if _, err := filetoken.ParseKeyset(c.TokenKeyset); err != nil {
		return fmt.Errorf("storage.tokenKeyset: %w", err)
	}
	if c.TokenTTLSeconds < 0 {
		return fmt.Errorf("storage.tokenTTLSeconds must be greater than zero, or unset for %d, got %d", DefaultTokenTTLSeconds, c.TokenTTLSeconds)
	}
	return nil
}

// validateBackend checks what this application adds to a core/storage
// backend's settings. New runs it as well, so a configuration that never went
// through Validate is refused the same way: a conflicting route prefix is not
// silently replaced, and a non-positive size cannot reach core's panic.
func (c Config) validateBackend() error {
	if p := c.Local.RoutePrefix; p != "" && p != RoutePrefix {
		return fmt.Errorf("storage.local.routePrefix must be %q, where this application mounts its storage routes, or left unset; got %q", RoutePrefix, p)
	}
	if len(c.AllowedUploadTypes) == 0 {
		return fmt.Errorf("storage.allowedUploadTypes must list at least one MIME type")
	}
	if len(c.InlineTypes) == 0 {
		return fmt.Errorf("storage.inlineTypes must list at least one MIME type")
	}
	// core/storage compares types to both lists exactly, so each must be a
	// bare, lower-case type/subtype: no parameters, no wildcard.
	for _, t := range c.AllowedUploadTypes {
		if err := corestorage.ValidMediaType(t); err != nil {
			return fmt.Errorf("storage.allowedUploadTypes: %w", err)
		}
	}
	for _, t := range c.InlineTypes {
		if err := corestorage.ValidMediaType(t); err != nil {
			return fmt.Errorf("storage.inlineTypes: %w", err)
		}
	}
	if err := corestorage.CheckUploadTypes(c.AllowedUploadTypes, c.InlineTypes); err != nil {
		return fmt.Errorf("storage.allowedUploadTypes and storage.inlineTypes: %w", err)
	}
	for client, types := range c.AllowedInternalUploadTypes {
		if strings.TrimSpace(client) == "" {
			return fmt.Errorf("storage.allowedInternalUploadTypes: a client name is empty")
		}
		for _, t := range types {
			if err := corestorage.ValidMediaType(t); err != nil {
				return fmt.Errorf("storage.allowedInternalUploadTypes.%s: %w", client, err)
			}
		}
	}
	if c.MaxUploadBytes <= 0 {
		return fmt.Errorf("storage.maxUploadBytes must be greater than zero, got %d", c.MaxUploadBytes)
	}
	return c.validateUploadTypeExtensions()
}

// validateUploadTypeExtensions checks that every type clients may upload, on
// either upload route, lists the extensions its file names may end in:
// core/storage checks no name whose type has none.
func (c Config) validateUploadTypeExtensions() error {
	if len(c.UploadTypeExtensions) == 0 {
		return fmt.Errorf("storage.uploadTypeExtensions must list the file name extensions of each type clients may upload")
	}
	if err := corestorage.CheckUploadTypeExtensions(c.UploadTypeExtensions); err != nil {
		return fmt.Errorf("storage.uploadTypeExtensions: %w", err)
	}
	uploadable := slices.Clone(c.AllowedUploadTypes)
	for _, client := range slices.Sorted(maps.Keys(c.AllowedInternalUploadTypes)) {
		uploadable = append(uploadable, c.AllowedInternalUploadTypes[client]...)
	}
	for _, t := range uploadable {
		if _, ok := c.UploadTypeExtensions[t]; !ok {
			return fmt.Errorf("storage.uploadTypeExtensions lists no extensions for %q, which clients may upload", t)
		}
	}
	return nil
}

// ProxyConfig configures proxy mode (storage.type: proxy): which service owns
// the files and where it serves its storage API.
type ProxyConfig struct {
	// Service is the owning service's ID in the outbound services registry
	// (config.yaml services), which supplies its URL, authentication and timeout.
	Service string `yaml:"service"`
	// UploadPath is the owning service's upload endpoint (POST) for the files
	// this deployment's users upload, which allocates a key and returns the
	// file metadata with an upload URL.
	UploadPath string `yaml:"uploadPath"`
	// InternalUploadPath is its upload endpoint (POST) for the files this
	// deployment generates and saves itself, such as a document. It answers as
	// UploadPath does, and is for a machine client.
	InternalUploadPath string `yaml:"internalUploadPath"`
	// DownloadPath is its download endpoint (GET), which returns a
	// download URL. Must contain {key}.
	DownloadPath string `yaml:"downloadPath"`
	// DeletePath is its delete endpoint (DELETE). Must contain {key}.
	DeletePath string `yaml:"deletePath"`
	// AllowedUploadTypes are the MIME types this deployment's users may upload;
	// any other is refused with 415 before the file is sent on. Each is compared
	// exactly with the upload's type, as the owning service compares it, and the
	// owning service's own list decides what it accepts. Required: this
	// deployment states what its own users may send. It does not apply to what
	// this deployment generates and saves itself.
	AllowedUploadTypes []string `yaml:"allowedUploadTypes"`
}

// Validate reports whether the proxy configuration is usable.
func (c ProxyConfig) Validate() error {
	if strings.TrimSpace(c.Service) == "" {
		return fmt.Errorf("storage.proxy.service is required when storage.type is %s", TypeProxy)
	}
	for name, path := range map[string]string{
		"storage.proxy.uploadPath":         c.UploadPath,
		"storage.proxy.internalUploadPath": c.InternalUploadPath,
		"storage.proxy.downloadPath":       c.DownloadPath,
		"storage.proxy.deletePath":         c.DeletePath,
	} {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("%s is required when storage.type is %s", name, TypeProxy)
		}
	}
	if !strings.Contains(c.DownloadPath, KeyPlaceholder) {
		return fmt.Errorf("storage.proxy.downloadPath must contain %s", KeyPlaceholder)
	}
	if !strings.Contains(c.DeletePath, KeyPlaceholder) {
		return fmt.Errorf("storage.proxy.deletePath must contain %s", KeyPlaceholder)
	}
	if len(c.AllowedUploadTypes) == 0 {
		return fmt.Errorf("storage.proxy.allowedUploadTypes must list at least one MIME type")
	}
	for _, t := range c.AllowedUploadTypes {
		if err := corestorage.ValidMediaType(t); err != nil {
			return fmt.Errorf("storage.proxy.allowedUploadTypes: %w", err)
		}
	}
	return nil
}
