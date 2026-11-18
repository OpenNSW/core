package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corestorage "github.com/OpenNSW/core/storage"
	"github.com/OpenNSW/core/storage/drivers"
)

// testUploadTypes, testInlineTypes and testMaxUploadBytes are the upload limits
// and the types a download opens in the browser that the test configurations
// set.
var (
	testUploadTypes = []string{"application/pdf", "image/png"}
	// testUploadTypeExtensions covers testUploadTypes and text/html, which
	// tests grant a machine client.
	testUploadTypeExtensions = map[string][]string{
		"application/pdf": {".pdf"},
		"image/png":       {".png"},
		"text/html":       {".html"},
	}
	testInlineTypes = corestorage.DefaultInlineTypes()
)

// testTokenKeyset is a valid storage.tokenKeyset, built rather than written
// out so no key literal sits in the source.
var testTokenKeyset = "test:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))

const testMaxUploadBytes = 1 << 20

// localConfig is a valid local-backend configuration whose URLs point at
// publicURL.
func localConfig(t *testing.T, publicURL string) Config {
	t.Helper()
	return Config{
		Config: corestorage.Config{
			Type: corestorage.TypeLocal,
			Local: drivers.LocalConfig{
				BaseDir:   t.TempDir(),
				PublicURL: publicURL,
				PutSecret: "secret",
			},
			PresignTTLSeconds: 900,
		},
		AllowedUploadTypes:   testUploadTypes,
		UploadTypeExtensions: testUploadTypeExtensions,
		InlineTypes:          testInlineTypes,
		MaxUploadBytes:       testMaxUploadBytes,
		TokenKeyset:          testTokenKeyset,
	}
}

// The local backend's upload URLs point under RoutePrefix, and the content
// routes the stack mounts serve them there.
func TestNew_LocalContentUnderRoutePrefix(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	stack, err := New(context.Background(), localConfig(t, srv.URL), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stack.LocalContent.RegisterRoutes(mux)

	content := []byte("%PDF-1.7")
	meta, err := stack.Service.Upload(context.Background(), "a.pdf", int64(len(content)), "application/pdf")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if want := srv.URL + RoutePrefix + "/" + meta.Key + "/content?"; !strings.HasPrefix(meta.UploadURL, want) {
		t.Fatalf("UploadURL = %s, want it under %s", meta.UploadURL, want)
	}

	req, err := http.NewRequest(http.MethodPut, meta.UploadURL, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/pdf")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("PUT %s = %d, want %d", meta.UploadURL, resp.StatusCode, http.StatusNoContent)
	}
}

func TestConfigValidate_LocalRoutePrefix(t *testing.T) {
	for _, prefix := range []string{"", RoutePrefix} {
		cfg := localConfig(t, "http://localhost:8080")
		cfg.Local.RoutePrefix = prefix
		if err := cfg.Validate(); err != nil {
			t.Errorf("routePrefix %q: unexpected error: %v", prefix, err)
		}
	}

	cfg := localConfig(t, "http://localhost:8080")
	cfg.Local.RoutePrefix = "/files"
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "storage.local.routePrefix") {
		t.Errorf("routePrefix /files: Validate() = %v, want an error naming storage.local.routePrefix", err)
	}
	// New refuses it too, rather than replacing it with RoutePrefix.
	if _, err := New(context.Background(), cfg, nil); err == nil || !strings.Contains(err.Error(), "storage.local.routePrefix") {
		t.Errorf("routePrefix /files: New() = %v, want an error naming storage.local.routePrefix", err)
	}
}

// The backend enforces the upload limits from the configuration, not core's
// defaults (any type, 32 MiB).
func TestNew_AppliesConfiguredUploadLimits(t *testing.T) {
	stack, err := New(context.Background(), localConfig(t, "http://localhost:8080"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	for _, mime := range testUploadTypes {
		if _, err := stack.Service.Upload(ctx, "file", 10, mime); err != nil {
			t.Errorf("Upload(%s) = %v, want it accepted", mime, err)
		}
	}
	for _, mime := range []string{"application/x-msdownload", "application/vnd.ms-excel", "text/html"} {
		if _, err := stack.Service.Upload(ctx, "file", 10, mime); !errors.Is(err, corestorage.ErrContentTypeNotAllowed) {
			t.Errorf("Upload(%s) = %v, want ErrContentTypeNotAllowed", mime, err)
		}
	}

	if _, err := stack.Service.Upload(ctx, "a.pdf", testMaxUploadBytes, "application/pdf"); err != nil {
		t.Errorf("Upload at maxUploadBytes = %v, want it accepted", err)
	}
	var tooLarge *corestorage.FileTooLargeError
	if _, err := stack.Service.Upload(ctx, "a.pdf", testMaxUploadBytes+1, "application/pdf"); !errors.As(err, &tooLarge) {
		t.Errorf("Upload over maxUploadBytes = %v, want *FileTooLargeError", err)
	}
}

func TestConfigValidate_UploadTypeExtensions(t *testing.T) {
	tests := []struct {
		name       string
		extensions map[string][]string
		internal   map[string][]string
		wantErr    string
	}{
		{name: "none", extensions: nil, wantErr: "storage.uploadTypeExtensions must list"},
		{name: "an extension in capitals", extensions: map[string][]string{"application/pdf": {".PDF"}, "image/png": {".png"}}, wantErr: "storage.uploadTypeExtensions"},
		{name: "a type with no extensions", extensions: map[string][]string{"application/pdf": {}, "image/png": {".png"}}, wantErr: "storage.uploadTypeExtensions"},
		{name: "an uploadable type left out", extensions: map[string][]string{"application/pdf": {".pdf"}}, wantErr: `lists no extensions for "image/png"`},
		{name: "a type granted on the internal route left out", extensions: map[string][]string{"application/pdf": {".pdf"}, "image/png": {".png"}},
			internal: map[string][]string{"npqs": {"text/html"}}, wantErr: `lists no extensions for "text/html"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := localConfig(t, "http://localhost:8080")
			cfg.UploadTypeExtensions = tt.extensions
			cfg.AllowedInternalUploadTypes = tt.internal
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %v, want an error containing %q", err, tt.wantErr)
			}
			// New refuses it the same way, instead of reaching core's panic.
			if _, err := New(context.Background(), cfg, nil); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New() = %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}

	t.Run("covering both routes", func(t *testing.T) {
		cfg := localConfig(t, "http://localhost:8080")
		cfg.InlineTypes = append(corestorage.DefaultInlineTypes(), "text/html")
		cfg.AllowedInternalUploadTypes = map[string][]string{"npqs": {"text/html"}}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})
}

func TestConfigValidate_UploadLimits(t *testing.T) {
	tests := []struct {
		name    string
		types   []string
		max     int64
		wantErr string
	}{
		{name: "no types", types: nil, max: 1, wantErr: "storage.allowedUploadTypes must list"},
		{name: "empty entry", types: []string{""}, max: 1, wantErr: "storage.allowedUploadTypes"},
		{name: "no subtype", types: []string{"pdf"}, max: 1, wantErr: "storage.allowedUploadTypes"},
		{name: "parameters", types: []string{"text/plain; charset=utf-8"}, max: 1, wantErr: "storage.allowedUploadTypes"},
		{name: "upper case", types: []string{"Application/PDF"}, max: 1, wantErr: "storage.allowedUploadTypes"},
		{name: "wildcard", types: []string{"image/*"}, max: 1, wantErr: "storage.allowedUploadTypes"},
		{name: "no size", types: testUploadTypes, max: 0, wantErr: "storage.maxUploadBytes"},
		{name: "negative size", types: testUploadTypes, max: -1, wantErr: "storage.maxUploadBytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := localConfig(t, "http://localhost:8080")
			cfg.AllowedUploadTypes = tt.types
			cfg.MaxUploadBytes = tt.max

			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %v, want an error containing %q", err, tt.wantErr)
			}
			// New refuses it the same way, instead of reaching core's panic
			// on a non-positive size.
			if _, err := New(context.Background(), cfg, nil); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New() = %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}

	t.Run("valid", func(t *testing.T) {
		if err := localConfig(t, "http://localhost:8080").Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})

	// The owning service enforces its own limits and decides what opens in the
	// browser in proxy mode, so neither is read there.
	t.Run("proxy mode needs none", func(t *testing.T) {
		cfg := Config{
			Config:      corestorage.Config{Type: TypeProxy},
			Proxy:       defaultProxyConfig(),
			TokenKeyset: testTokenKeyset,
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})
}

func TestConfigValidate_InlineTypes(t *testing.T) {
	tests := []struct {
		name    string
		inline  []string
		upload  []string
		wantErr string // empty: valid
	}{
		{name: "no types", inline: nil, wantErr: "storage.inlineTypes must list"},
		{name: "empty entry", inline: []string{""}, wantErr: "storage.inlineTypes"},
		{name: "no subtype", inline: []string{"pdf"}, wantErr: "storage.inlineTypes"},
		{name: "parameters", inline: []string{"text/html; charset=utf-8"}, wantErr: "storage.inlineTypes"},
		{name: "upper case", inline: []string{"Application/PDF"}, wantErr: "storage.inlineTypes"},
		{name: "wildcard", inline: []string{"image/*"}, wantErr: "storage.inlineTypes"},
		{name: "html opens in the browser and clients may upload it", inline: []string{"application/pdf", "text/html"}, upload: []string{"application/pdf", "text/html"}, wantErr: "text/html"},
		{name: "html opens in the browser and no clients may upload it", inline: []string{"application/pdf", "text/html"}},
		{name: "a default type clients may upload", inline: []string{"application/pdf"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := localConfig(t, "http://localhost:8080")
			cfg.InlineTypes = tt.inline
			if tt.upload != nil {
				cfg.AllowedUploadTypes = tt.upload
			}

			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %v, want an error containing %q", err, tt.wantErr)
			}
			// New refuses it the same way, instead of reaching core's panic.
			if _, err := New(context.Background(), cfg, nil); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New() = %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}

// The backend opens in the browser the types the configuration lists and saves
// every other file, whatever its name: a service that stores HTML it generates
// itself lists text/html.
func TestNew_AppliesConfiguredInlineTypes(t *testing.T) {
	tests := []struct {
		name   string
		inline []string
		file   string
		mime   string
		want   string
	}{
		{name: "default list opens a PDF", inline: testInlineTypes, file: "a.pdf", mime: "application/pdf", want: drivers.DispositionInline},
		{name: "default list saves generated HTML", inline: testInlineTypes, file: "permit.html", mime: "text/html; charset=utf-8", want: drivers.DispositionAttachment},
		{name: "default list saves XML", inline: testInlineTypes, file: "a.xml", mime: "text/xml", want: drivers.DispositionAttachment},
		{name: "listed HTML opens", inline: append([]string{"text/html"}, testInlineTypes...), file: "permit.html", mime: "text/html; charset=utf-8", want: drivers.DispositionInline},
		{name: "a type not listed is saved", inline: []string{"text/html"}, file: "a.pdf", mime: "application/pdf", want: drivers.DispositionAttachment},
		{name: "the extension decides nothing", inline: testInlineTypes, file: "draft.pdf", mime: "text/xml", want: drivers.DispositionAttachment},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := localConfig(t, "http://localhost:8080")
			cfg.InlineTypes = tt.inline
			stack, err := New(context.Background(), cfg, nil)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			meta, err := stack.Service.Save(context.Background(), tt.file, tt.mime, strings.NewReader("data"), 4)
			if err != nil {
				t.Fatalf("Save: %v", err)
			}
			dl, err := stack.Service.DownloadURL(context.Background(), meta.Key)
			if err != nil {
				t.Fatalf("DownloadURL: %v", err)
			}
			if dl.Disposition != tt.want || dl.ContentType != tt.mime {
				t.Errorf("DownloadURL = type %q, disposition %q; want %q, %q", dl.ContentType, dl.Disposition, tt.mime, tt.want)
			}
		})
	}
}

// Every mode issues file tokens, so every mode needs the token settings.
func TestConfigValidate_FileTokens(t *testing.T) {
	proxy := Config{Config: corestorage.Config{Type: TypeProxy}, Proxy: defaultProxyConfig()}
	backend := localConfig(t, "http://localhost:8080")

	tests := []struct {
		name    string
		keyset  string
		ttl     int
		wantErr string
		wantTTL time.Duration
	}{
		{name: "no keyset", keyset: "", wantErr: "storage.tokenKeyset is required"},
		{name: "malformed keyset", keyset: "test:not-base64!", wantErr: "storage.tokenKeyset"},
		{name: "short key", keyset: "test:" + base64.StdEncoding.EncodeToString([]byte("short")), wantErr: "storage.tokenKeyset"},
		{name: "negative TTL", keyset: testTokenKeyset, ttl: -1, wantErr: "storage.tokenTTLSeconds"},
		{name: "default TTL", keyset: testTokenKeyset, wantTTL: time.Hour},
		{name: "set TTL", keyset: testTokenKeyset, ttl: 600, wantTTL: 10 * time.Minute},
	}
	for _, tt := range tests {
		for mode, cfg := range map[string]Config{"proxy": proxy, "backend": backend} {
			t.Run(tt.name+" in "+mode+" mode", func(t *testing.T) {
				cfg.TokenKeyset, cfg.TokenTTLSeconds = tt.keyset, tt.ttl
				err := cfg.Validate()
				if tt.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
						t.Errorf("Validate() = %v, want an error containing %q", err, tt.wantErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				if got := cfg.TokenTTL(); got != tt.wantTTL {
					t.Errorf("TokenTTL() = %v, want %v", got, tt.wantTTL)
				}
			})
		}
	}
}
