package staticdata

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/httputil"
	"github.com/OpenNSW/core/pagination"
)

// cacheControlImmutable tells the caller's own browser it never needs to
// re-fetch a given (id, manifestVersion) response, since a published manifest
// never changes what it lists. A manifest.json source (a checkout of the
// artifacts repository) loads as manifest version 1 and can change, so after
// a static-data change there a browser needs a hard reload. "private" (not
// "public") because this endpoint is scope-protected — a shared/CDN cache must
// never serve one caller's response to another. Search responses are cached
// the same way: the query string is part of the URL.
const cacheControlImmutable = "private, max-age=31536000, immutable"

// Handler serves static JSON reference-data artifacts looked up by id, as an
// artifact manifest version lists them.
type Handler struct {
	artifacts *artifact.Set
}

// NewHandler creates a new static data HTTP handler.
func NewHandler(artifacts *artifact.Set) *Handler {
	return &Handler{artifacts: artifacts}
}

// parseManifestVersion reads the required manifestVersion query parameter, a
// positive integer.
func parseManifestVersion(r *http.Request) (int, error) {
	q := r.URL.Query()
	if !q.Has("manifestVersion") {
		return 0, errors.New("manifestVersion query parameter is required")
	}
	n, err := strconv.Atoi(q.Get("manifestVersion"))
	if err != nil || n <= 0 {
		return 0, errors.New("manifestVersion must be a positive integer")
	}
	return n, nil
}

// HandleGet handles GET /api/v1/static-data/{id}?manifestVersion=.
// Without q, offset, or limit it returns the raw artifact. With any of those
// it loads the artifact, applies parent when present, ranks matches on the
// searchKeys row keys (comma-separated; default title,const), and returns one
// page of the original rows.
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	manifestVersion, err := parseManifestVersion(r)
	if err != nil {
		httputil.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}

	if isSearch(r) {
		h.handleSearch(w, r, id, manifestVersion)
		return
	}

	raw, err := LoadAt(r.Context(), h.artifacts, id, manifestVersion)
	if err != nil {
		writeLoadError(w, r, err, id, manifestVersion)
		return
	}

	w.Header().Set("Cache-Control", cacheControlImmutable)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw) //nolint:gosec // G705 false positive: raw is validated JSON (loadable.Parse), served as application/json, not HTML.
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request, id string, manifestVersion int) {
	offset, limit, err := pagination.ParsePaginationParams(r)
	if err != nil {
		slog.WarnContext(r.Context(), "invalid pagination parameters", "error", err)
		httputil.Error(w, r, http.StatusBadRequest, "invalid pagination parameters")
		return
	}
	finalOffset, finalLimit := pagination.ResolvePaginationParams(offset, limit)

	searchKeys, err := parseSearchKeys(r.URL.Query().Get("searchKeys"))
	if err != nil {
		httputil.Error(w, r, http.StatusBadRequest, err.Error())
		return
	}

	raw, err := LoadAt(r.Context(), h.artifacts, id, manifestVersion)
	if err != nil {
		writeLoadError(w, r, err, id, manifestVersion)
		return
	}

	result, err := Search(raw, strings.TrimSpace(r.URL.Query().Get("q")), r.URL.Query().Get("parent"), searchKeys, finalOffset, finalLimit)
	if err != nil {
		httputil.InternalServerError(w, r, "failed to search static data", err, "id", id, "manifestVersion", manifestVersion)
		return
	}

	w.Header().Set("Cache-Control", cacheControlImmutable)
	httputil.JSON(w, http.StatusOK, result)
}

// searchKeyPattern is the shape of a row key a search may name: the same characters an
// x-search.displayTemplate placeholder accepts, so a key can never hold the comma that
// separates searchKeys.
var searchKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// maxSearchKeys bounds how many keys one search may name. A search reads every named key of
// every row, so an unbounded list would let a short request cost far more than the search it
// asks for. A field names a few keys.
const maxSearchKeys = 10

// parseSearchKeys reads the comma-separated searchKeys parameter. Empty entries are ignored,
// a key named again is kept once at its first position, and none at all means Search's default
// keys. At most maxSearchKeys different keys are accepted.
func parseSearchKeys(param string) ([]string, error) {
	var keys []string
	seen := make(map[string]struct{})
	for _, key := range strings.Split(param, ",") {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if !searchKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("invalid searchKeys entry %q: use letters, digits and underscores", key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		if len(keys) == maxSearchKeys {
			return nil, fmt.Errorf("too many searchKeys: at most %d", maxSearchKeys)
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys, nil
}

func isSearch(r *http.Request) bool {
	q := r.URL.Query()
	return q.Has("q") || q.Has("offset") || q.Has("limit")
}

func writeLoadError(w http.ResponseWriter, r *http.Request, err error, id string, manifestVersion int) {
	if errors.Is(err, artifact.ErrNotFound) || errors.Is(err, artifact.ErrUnknownManifestVersion) {
		httputil.Error(w, r, http.StatusNotFound, "static data not found")
		return
	}
	httputil.InternalServerError(w, r, "failed to load static data", err, "id", id, "manifestVersion", manifestVersion)
}
