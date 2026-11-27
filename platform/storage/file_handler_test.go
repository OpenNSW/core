// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	corestorage "github.com/OpenNSW/core/storage"
	"github.com/OpenNSW/core/storage/drivers"

	"github.com/OpenNSW/core/platform/audit"
	"github.com/OpenNSW/core/platform/authn"
	"github.com/OpenNSW/core/platform/storage/fileaccess"
	"github.com/OpenNSW/core/platform/storage/filetoken"
)

// testFileAccess returns real file tokens over testTokenKeyset, on a clock the
// test can move.
func testFileAccess(t *testing.T) (*fileaccess.Access, *time.Time) {
	t.Helper()
	now := time.Now()
	keys, err := filetoken.ParseKeyset(testTokenKeyset)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := filetoken.NewCodec(keys, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	access, err := fileaccess.New(codec, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return access, &now
}

func user(sub string) *authn.Principal {
	return &authn.Principal{Kind: authn.KindUser, IDPUserID: sub}
}

// download calls h.Download for ref as p (no principal when p is nil).
func download(h *FileHandler, ref string, p *authn.Principal) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/storage/"+url.PathEscape(ref), nil)
	req.SetPathValue("key", ref)
	if p != nil {
		req = req.WithContext(authn.ContextWithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	h.Download(rec, req)
	return rec
}

func assertDownloadURL(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		DownloadURL string `json:"download_url"`
		ExpiresAt   int64  `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.DownloadURL == "" || body.ExpiresAt <= time.Now().Unix() {
		t.Fatalf("response = %+v (%v), want a download_url and a future expires_at", body, err)
	}
}

// assertFileType checks that a successful download gives the file's
// extension, the type it was stored with and how the URL serves it, and no
// name for it.
func assertFileType(t *testing.T, rec *httptest.ResponseRecorder, ext, mimeType, disposition string) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["extension"] != ext || body["mime_type"] != mimeType || body["disposition"] != disposition {
		t.Errorf("extension, mime_type, disposition = %v, %v, %v; want %q, %q, %q", body["extension"], body["mime_type"], body["disposition"], ext, mimeType, disposition)
	}
	if _, ok := body["name"]; ok {
		t.Errorf("response %s names the file", rec.Body.String())
	}
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, want, rec.Body.String())
	}
}

// storedKey stores a PDF with svc and returns its key. A download is only
// issued for a file the backend holds.
func storedKey(t *testing.T, svc Service) string {
	t.Helper()
	meta, err := svc.Save(context.Background(), "a.pdf", "application/pdf", strings.NewReader("%PDF-1.4"), 8)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	return meta.Key
}

// reservedKey reserves a key with svc, as an upload would, without storing a
// file under it.
func reservedKey(t *testing.T, svc Service) string {
	t.Helper()
	meta, err := svc.Upload(context.Background(), "a.pdf", 10, "application/pdf")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	return meta.Key
}

func TestFileHandler_Download(t *testing.T) {
	stack, err := New(context.Background(), localConfig(t, "http://localhost:8080"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	access, now := testFileAccess(t)
	h := NewFileHandler(stack.Service, access)
	key := storedKey(t, stack.Service)
	token, err := access.Issue(user("alice"), key, fileaccess.ScopeRead)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("the holder's token", func(t *testing.T) {
		rec := download(h, token, user("alice"))
		assertDownloadURL(t, rec)
		assertFileType(t, rec, ".pdf", "application/pdf", drivers.DispositionInline)
	})
	t.Run("a file the backend does not hold", func(t *testing.T) {
		missing, err := access.Issue(user("alice"), reservedKey(t, stack.Service), fileaccess.ScopeRead)
		if err != nil {
			t.Fatal(err)
		}
		assertStatus(t, download(h, missing, user("alice")), http.StatusNotFound)
	})
	t.Run("someone else's token", func(t *testing.T) {
		assertStatus(t, download(h, token, user("bob")), http.StatusForbidden)
	})
	t.Run("a token that can't be opened", func(t *testing.T) {
		submitOnly, err := access.Issue(user("alice"), key, fileaccess.ScopeSubmit)
		if err != nil {
			t.Fatal(err)
		}
		assertStatus(t, download(h, submitOnly, user("alice")), http.StatusForbidden)
	})
	// A raw storage key grants nothing, even to whoever uploaded the file.
	t.Run("a stored value", func(t *testing.T) {
		assertStatus(t, download(h, key, user("alice")), http.StatusForbidden)
	})
	t.Run("anything else that is not a reference", func(t *testing.T) {
		assertStatus(t, download(h, "../outside", user("alice")), http.StatusForbidden)
	})
	t.Run("no principal", func(t *testing.T) {
		assertStatus(t, download(h, token, nil), http.StatusUnauthorized)
	})
	t.Run("an expired token", func(t *testing.T) {
		*now = now.Add(time.Hour)
		assertStatus(t, download(h, token, user("alice")), http.StatusGone)
	})
}

type failingRefs struct{}

func (failingRefs) Issue(*authn.Principal, string, ...fileaccess.Scope) (string, error) {
	return "", errors.New("keyset unavailable")
}

func (failingRefs) Resolve(*authn.Principal, string, fileaccess.Scope) (string, error) {
	return "", errors.New("keyset unavailable")
}

func TestFileHandler_DownloadResolveFailure(t *testing.T) {
	stack, err := New(context.Background(), localConfig(t, "http://localhost:8080"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	assertStatus(t, download(NewFileHandler(stack.Service, failingRefs{}), "ref", user("alice")), http.StatusInternalServerError)
}

// In proxy mode the stored value is the owning service's own reference, and
// its answers are relayed as the proxy's own handler relays them.
func TestFileHandler_DownloadThroughProxy(t *testing.T) {
	access, _ := testFileAccess(t)

	t.Run("the holder's token", func(t *testing.T) {
		stack := newProxyStack(t, ownerToken)
		h := NewFileHandler(stack.Service, access)
		token, err := access.Issue(user("alice"), storedKey(t, stack.Service), fileaccess.ScopeRead)
		if err != nil {
			t.Fatal(err)
		}
		rec := download(h, token, user("alice"))
		assertDownloadURL(t, rec)
		// What the owning service says about the file is relayed as it said it.
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["mime_type"] != "application/pdf" || body["disposition"] != drivers.DispositionInline {
			t.Errorf("mime_type, disposition = %v, %v; want application/pdf, %s", body["mime_type"], body["disposition"], drivers.DispositionInline)
		}
	})
	t.Run("a file the owner does not hold", func(t *testing.T) {
		stack := newProxyStack(t, ownerToken)
		h := NewFileHandler(stack.Service, access)
		token, err := access.Issue(user("alice"), reservedKey(t, stack.Service), fileaccess.ScopeRead)
		if err != nil {
			t.Fatal(err)
		}
		assertStatus(t, download(h, token, user("alice")), http.StatusNotFound)
	})
	t.Run("the owner's rejection is relayed", func(t *testing.T) {
		h := NewFileHandler(newProxyStack(t, ownerToken).Service, access)
		token, err := access.Issue(user("alice"), "not-a-key", fileaccess.ScopeRead)
		if err != nil {
			t.Fatal(err)
		}
		assertStatus(t, download(h, token, user("alice")), http.StatusBadRequest)
	})
	t.Run("the owner refusing credentials is a gateway failure", func(t *testing.T) {
		h := NewFileHandler(newProxyStack(t, "wrong-token").Service, access)
		token, err := access.Issue(user("alice"), "any-key", fileaccess.ScopeRead)
		if err != nil {
			t.Fatal(err)
		}
		assertStatus(t, download(h, token, user("alice")), http.StatusBadGateway)
	})
}

// newFileAccess returns file tokens over its own one-key keyset filled with b.
func newFileAccess(t *testing.T, b byte) *fileaccess.Access {
	t.Helper()
	keys, err := filetoken.ParseKeyset("k:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	codec, err := filetoken.NewCodec(keys, nil)
	if err != nil {
		t.Fatal(err)
	}
	access, err := fileaccess.New(codec, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return access
}

// An agency runs the same handler in proxy mode. What it stores is the token
// TNSW gave it, which it hands its officer wrapped in a token of its own; the
// officer's download resolves both, one at each end.
func TestFileHandler_AgencyOfficerDownloadsThroughTNSW(t *testing.T) {
	const agencyClient = "CDA_TO_NSW"
	tnswAccess, agencyAccess := newFileAccess(t, 1), newFileAccess(t, 2)

	// TNSW: its own backend, with the agency calling as its machine client.
	mux := http.NewServeMux()
	tnsw := httptest.NewServer(mux)
	t.Cleanup(tnsw.Close)
	driver, err := drivers.NewLocalFSDriver(t.TempDir(), tnsw.URL, "owner-secret", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	tnswService := corestorage.NewService(driver, corestorage.WithAllowedUploadTypes(testUploadTypes...))
	tnswFiles := NewFileHandler(tnswService, tnswAccess)
	asAgency := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			client := &authn.Principal{Kind: authn.KindClient, ClientID: agencyClient}
			next(w, r.WithContext(authn.ContextWithPrincipal(r.Context(), client)))
		}
	}
	mux.HandleFunc(UploadRoute, asAgency(tnswFiles.Upload))
	mux.HandleFunc(DownloadRoute, asAgency(tnswFiles.Download))
	corestorage.NewLocalContentHandler(driver).RegisterRoutes(mux)

	key := storedKey(t, tnswService)
	pushed, err := tnswAccess.IssueForClient(agencyClient, key, fileaccess.ScopeRead)
	if err != nil {
		t.Fatal(err)
	}

	// The agency: a proxy onto TNSW.
	agencyService, err := NewProxyService(newRegistry(t, tnsw.URL, ownerToken), defaultProxyConfig())
	if err != nil {
		t.Fatal(err)
	}
	agencyFiles := NewFileHandler(agencyService, agencyAccess)
	officer := user("cda-officer")
	officerToken, err := agencyAccess.Issue(officer, pushed, fileaccess.ScopeRead)
	if err != nil {
		t.Fatal(err)
	}

	rec := download(agencyFiles, officerToken, officer)
	assertDownloadURL(t, rec)
	// Only TNSW knows the file behind what the agency stores; the agency
	// relays what TNSW says it is.
	assertFileType(t, rec, ".pdf", "application/pdf", drivers.DispositionInline)
	assertStatus(t, download(agencyFiles, officerToken, user("another-officer")), http.StatusForbidden)

	// An officer's own upload goes to TNSW too: TNSW issues the agency a
	// token, and the agency wraps it for the officer.
	uploaded := upload(agencyFiles, officer, `{"filename":"cert.pdf","mime_type":"application/pdf","size":10}`)
	assertStatus(t, uploaded, http.StatusOK)
	var meta struct {
		Key       string `json:"key"`
		UploadURL string `json:"upload_url"`
	}
	if err := json.NewDecoder(uploaded.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	putFile(t, meta.UploadURL, "application/pdf", 10)
	stored, err := agencyAccess.Resolve(officer, meta.Key, fileaccess.ScopeSubmit)
	if err != nil {
		t.Fatalf("the officer's upload reference: %v", err)
	}
	if _, err := tnswAccess.Resolve(&authn.Principal{Kind: authn.KindClient, ClientID: agencyClient}, stored, fileaccess.ScopeRead); err != nil {
		t.Fatalf("what the agency stores is not TNSW's token for it: %v", err)
	}
	// The upload reference itself can only be submitted. Once the form is
	// saved, the officer reads the file with the reference the task gives.
	assertStatus(t, download(agencyFiles, meta.Key, officer), http.StatusForbidden)
	read, err := agencyAccess.Issue(officer, stored, fileaccess.ScopeRead)
	if err != nil {
		t.Fatal(err)
	}
	assertDownloadURL(t, download(agencyFiles, read, officer))
}

// upload calls h.Upload with body as p (no principal when p is nil).
func upload(h *FileHandler, p *authn.Principal, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage", strings.NewReader(body))
	if p != nil {
		req = req.WithContext(authn.ContextWithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	h.Upload(rec, req)
	return rec
}

// putFile uploads size bytes to uploadURL as contentType, as a client does with
// the URL an upload returned.
func putFile(t *testing.T, uploadURL, contentType string, size int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(make([]byte, size)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT upload URL: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT upload URL status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}

func TestFileHandler_UploadReturnsAReference(t *testing.T) {
	// The content routes serve the upload URLs it returns.
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	stack, err := New(context.Background(), localConfig(t, srv.URL), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stack.LocalContent.RegisterRoutes(mux)
	access, _ := testFileAccess(t)
	h := NewFileHandler(stack.Service, access)

	rec := upload(h, user("alice"), `{"filename":"invoice.pdf","mime_type":"application/pdf","size":10}`)
	assertStatus(t, rec, http.StatusOK)
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["id"]; ok {
		t.Errorf("response %v has an id, which is the stored key without its extension", body)
	}
	if body["name"] != "invoice.pdf" || body["upload_url"] == "" {
		t.Errorf("response %v, want the name and an upload_url", body)
	}
	ref, _ := body["key"].(string)
	stored, err := access.Resolve(user("alice"), ref, fileaccess.ScopeSubmit)
	if err != nil {
		t.Fatalf("key %q is not a reference for the uploader: %v", ref, err)
	}
	if !strings.Contains(body["upload_url"].(string), stored) {
		t.Errorf("upload_url %v does not name the stored file %q", body["upload_url"], stored)
	}
	// It can only be submitted: opening the file takes the reference a task
	// gives its reader.
	rec = download(h, ref, user("alice"))
	assertStatus(t, rec, http.StatusForbidden)
	if !strings.Contains(rec.Body.String(), "save the form to view it") {
		t.Errorf("body %s, want it to say to save the form to view the file", rec.Body.String())
	}
	assertStatus(t, download(h, ref, user("bob")), http.StatusForbidden)
	read, err := access.Issue(user("alice"), stored, fileaccess.ScopeRead)
	if err != nil {
		t.Fatal(err)
	}
	// Until the file is uploaded there is nothing to open.
	assertStatus(t, download(h, read, user("alice")), http.StatusNotFound)
	putFile(t, body["upload_url"].(string), "application/pdf", 10)
	assertDownloadURL(t, download(h, read, user("alice")))
}

// captureAuditor records the last audit event.
type captureAuditor struct{ last audit.Event }

func (c *captureAuditor) Audit(_ context.Context, e audit.Event) { c.last = e }

func TestFileHandler_UploadPrepareIsAudited(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	stack, err := New(context.Background(), localConfig(t, srv.URL), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stack.LocalContent.RegisterRoutes(mux)
	access, _ := testFileAccess(t)
	cap := &captureAuditor{}
	h := NewFileHandler(stack.Service, access, WithAuditor(cap))
	rec := upload(h, user("alice"), `{"filename":"invoice.PDF","mime_type":"application/pdf","size":10}`)
	assertStatus(t, rec, http.StatusOK)

	if cap.last.EventType != audit.EventStorage || cap.last.Action != audit.ActionPresignUpload {
		t.Fatalf("audit = %+v, want STORAGE PRESIGN_UPLOAD", cap.last)
	}
	if cap.last.Failure || cap.last.TargetID == "" {
		t.Fatalf("audit failure=%v targetID=%q, want success with a storage key", cap.last.Failure, cap.last.TargetID)
	}
	if cap.last.Metadata["mime_type"] != "application/pdf" || cap.last.Metadata["extension"] != ".pdf" {
		t.Errorf("metadata = %v, want mime application/pdf extension .pdf", cap.last.Metadata)
	}
	if _, ok := cap.last.Metadata["kind"]; ok {
		t.Errorf("metadata names the route kind: %v", cap.last.Metadata)
	}
	if _, ok := cap.last.Metadata["client_id"]; ok {
		t.Errorf("metadata repeats the actor as client_id: %v", cap.last.Metadata)
	}
	var uploaded map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	issued, _ := uploaded["key"].(string)
	if cap.last.Metadata["reference_sha256"] != hashReference(issued) {
		t.Errorf("reference_sha256 = %v, want the hash of the reference issued to the caller", cap.last.Metadata["reference_sha256"])
	}
	if _, ok := cap.last.Metadata["filename"]; ok {
		t.Errorf("metadata names the filename: %v", cap.last.Metadata)
	}

	cap.last = audit.Event{}
	assertStatus(t, upload(h, user("alice"), `{"filename":"x.zip","mime_type":"application/zip","size":10}`), http.StatusUnsupportedMediaType)
	if !cap.last.Failure || cap.last.Action != audit.ActionPresignUpload || cap.last.Metadata["reason"] != "content_type_not_allowed" {
		t.Fatalf("refused upload audit = %+v, want failed PRESIGN_UPLOAD with reason content_type_not_allowed", cap.last)
	}
	if _, ok := cap.last.Metadata["error"]; ok {
		t.Errorf("metadata carries error text: %v", cap.last.Metadata)
	}
}

func TestFileHandler_DownloadIsAudited(t *testing.T) {
	stack, err := New(context.Background(), localConfig(t, "http://localhost:8080"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	access, now := testFileAccess(t)
	cap := &captureAuditor{}
	h := NewFileHandler(stack.Service, access, WithAuditor(cap))

	key := storedKey(t, stack.Service)
	token, err := access.Issue(user("alice"), key, fileaccess.ScopeRead)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := access.Issue(user("alice"), reservedKey(t, stack.Service), fileaccess.ScopeRead)
	if err != nil {
		t.Fatal(err)
	}
	submitOnly, err := access.Issue(user("alice"), key, fileaccess.ScopeSubmit)
	if err != nil {
		t.Fatal(err)
	}

	// refused checks the audit record of a download the handler answered with
	// status: a failed STORAGE READ with the given reason and no target,
	// because a reference that did not resolve names no stored file.
	refused := func(name, ref string, p *authn.Principal, status int, reason string) {
		t.Helper()
		cap.last = audit.Event{}
		assertStatus(t, download(h, ref, p), status)
		e := cap.last
		if e.EventType != audit.EventStorage || e.Action != audit.ActionRead || e.TargetType != audit.TargetStorage {
			t.Fatalf("%s: audit = %+v, want STORAGE READ on STORAGE_OBJECT", name, e)
		}
		if !e.Failure || e.Metadata["reason"] != reason {
			t.Errorf("%s: failure=%v reason=%v, want a failure with reason %q", name, e.Failure, e.Metadata["reason"], reason)
		}
		if _, ok := e.Metadata["error"]; ok {
			t.Errorf("%s: metadata carries error text: %v", name, e.Metadata)
		}
		metaJSON, _ := json.Marshal(e.Metadata)
		if e.TargetID == ref || strings.Contains(string(metaJSON), ref) {
			t.Errorf("%s: audit names the reference: %+v", name, e)
		}
	}

	// A refusal before the reference resolves has no target.
	t.Run("someone else's reference", func(t *testing.T) {
		refused("not yours", token, user("bob"), http.StatusForbidden, "not_yours")
		if cap.last.TargetID != "" {
			t.Errorf("TargetID = %q, want empty", cap.last.TargetID)
		}
	})
	t.Run("not a reference", func(t *testing.T) {
		refused("invalid", key, user("alice"), http.StatusForbidden, "invalid_reference")
		if cap.last.TargetID != "" {
			t.Errorf("TargetID = %q, want empty", cap.last.TargetID)
		}
	})
	t.Run("wrong scope", func(t *testing.T) {
		refused("scope", submitOnly, user("alice"), http.StatusForbidden, "wrong_scope")
		if cap.last.TargetID != "" {
			t.Errorf("TargetID = %q, want empty", cap.last.TargetID)
		}
	})
	t.Run("a file the backend does not hold", func(t *testing.T) {
		refused("not found", missing, user("alice"), http.StatusNotFound, "not_found")
		if cap.last.TargetID == "" {
			t.Error("TargetID is empty, want the storage key the reference resolved to")
		}
		if cap.last.Metadata["reference_sha256"] != hashReference(missing) {
			t.Errorf("reference_sha256 = %v, want the hash of the reference that resolved", cap.last.Metadata["reference_sha256"])
		}
	})
	t.Run("a link issued", func(t *testing.T) {
		cap.last = audit.Event{}
		assertDownloadURL(t, download(h, token, user("alice")))
		e := cap.last
		if e.EventType != audit.EventStorage || e.Action != audit.ActionRead || e.TargetType != audit.TargetStorage {
			t.Fatalf("audit = %+v, want STORAGE READ on STORAGE_OBJECT", e)
		}
		if e.Failure || e.TargetID != key {
			t.Errorf("failure=%v targetID=%q, want success on %q", e.Failure, e.TargetID, key)
		}
		if exp, _ := e.Metadata["expires_at"].(int64); exp <= time.Now().Unix() {
			t.Errorf("expires_at = %v, want a future time", e.Metadata["expires_at"])
		}
		if e.Metadata["reference_sha256"] != hashReference(token) {
			t.Errorf("reference_sha256 = %v, want the hash of the reference that resolved", e.Metadata["reference_sha256"])
		}
		if e.TargetID == token || e.Metadata["reference_sha256"] == token {
			t.Errorf("audit names the reference: %+v", e)
		}
	})
	t.Run("an expired reference", func(t *testing.T) {
		*now = now.Add(time.Hour)
		refused("expired", token, user("alice"), http.StatusGone, "expired")
	})
	t.Run("no principal is not audited", func(t *testing.T) {
		cap.last = audit.Event{}
		assertStatus(t, download(h, token, nil), http.StatusUnauthorized)
		if cap.last.Action != "" {
			t.Errorf("audit = %+v, want none", cap.last)
		}
	})
}

func TestFileHandler_DownloadFailuresAreAudited(t *testing.T) {
	stack, err := New(context.Background(), localConfig(t, "http://localhost:8080"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cap := &captureAuditor{}
	assertStatus(t, download(NewFileHandler(stack.Service, failingRefs{}, WithAuditor(cap)), "ref", user("alice")), http.StatusInternalServerError)
	if !cap.last.Failure || cap.last.Action != audit.ActionRead || cap.last.Metadata["reason"] != "resolve_failed" || cap.last.TargetID != "" {
		t.Errorf("audit = %+v, want a failed READ with reason resolve_failed and no target", cap.last)
	}
}

// In proxy mode the stored value is the owning service's file reference, so
// the records carry its SHA-256 instead.
func TestFileHandler_ProxyRecordsAHashOfTheReference(t *testing.T) {
	access, _ := testFileAccess(t)
	stack := newProxyStack(t, ownerToken)
	cap := &captureAuditor{}
	h := NewFileHandler(stack.Service, access, WithAuditor(cap))
	hashOf := func(v string) string {
		sum := sha256.Sum256([]byte(v))
		return hex.EncodeToString(sum[:])
	}

	t.Run("download", func(t *testing.T) {
		ownerRef := storedKey(t, stack.Service)
		token, err := access.Issue(user("alice"), ownerRef, fileaccess.ScopeRead)
		if err != nil {
			t.Fatal(err)
		}
		assertDownloadURL(t, download(h, token, user("alice")))
		if cap.last.Action != audit.ActionRead || cap.last.Failure || cap.last.TargetID != hashOf(ownerRef) || cap.last.Metadata["reference_sha256"] != hashOf(ownerRef) {
			t.Errorf("audit = %+v, want a READ on the hash of the owner's reference", cap.last)
		}
		metaJSON, _ := json.Marshal(cap.last.Metadata)
		if cap.last.TargetID == ownerRef || strings.Contains(string(metaJSON), ownerRef) {
			t.Errorf("audit names the owner's reference: %+v", cap.last)
		}
	})
	t.Run("a refused link for a file the owner does not hold", func(t *testing.T) {
		ownerRef := reservedKey(t, stack.Service)
		token, err := access.Issue(user("alice"), ownerRef, fileaccess.ScopeRead)
		if err != nil {
			t.Fatal(err)
		}
		assertStatus(t, download(h, token, user("alice")), http.StatusNotFound)
		if !cap.last.Failure || cap.last.Metadata["reason"] != "not_found" || cap.last.TargetID != hashOf(ownerRef) || cap.last.Metadata["reference_sha256"] != hashOf(ownerRef) {
			t.Errorf("audit = %+v, want a failed READ (not_found) on the hash of the owner's reference", cap.last)
		}
		metaJSON, _ := json.Marshal(cap.last.Metadata)
		if _, ok := cap.last.Metadata["error"]; ok || strings.Contains(string(metaJSON), ownerRef) {
			t.Errorf("audit names the owner's reference or carries error text: %+v", cap.last)
		}
	})
	t.Run("upload prepare", func(t *testing.T) {
		rec := upload(h, user("alice"), `{"filename":"a.pdf","mime_type":"application/pdf","size":10}`)
		assertStatus(t, rec, http.StatusOK)
		if cap.last.Action != audit.ActionPresignUpload || cap.last.Failure || len(cap.last.TargetID) != sha256.Size*2 || cap.last.Metadata["reference_sha256"] != cap.last.TargetID {
			t.Fatalf("audit = %+v, want a PRESIGN_UPLOAD on a SHA-256 hex digest", cap.last)
		}
	})
}

func TestFileHandler_UploadRejects(t *testing.T) {
	stack, err := New(context.Background(), localConfig(t, "http://localhost:8080"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	access, _ := testFileAccess(t)
	h := NewFileHandler(stack.Service, access)

	tests := []struct {
		name, body  string
		p           *authn.Principal
		wantStatus  int
		wantMessage string
	}{
		{"no principal", `{"filename":"a.pdf","mime_type":"application/pdf","size":10}`, nil, http.StatusUnauthorized, "authentication required"},
		{"bad body", `{`, user("alice"), http.StatusBadRequest, "invalid request body"},
		{"no filename", `{"mime_type":"application/pdf","size":10}`, user("alice"), http.StatusBadRequest, "filename is required"},
		{"no type", `{"filename":"a.pdf","size":10}`, user("alice"), http.StatusBadRequest, "mime_type is required"},
		{"no size", `{"filename":"a.pdf","mime_type":"application/pdf"}`, user("alice"), http.StatusBadRequest, "size must be greater than 0"},
		{"too large", fmt.Sprintf(`{"filename":"a.pdf","mime_type":"application/pdf","size":%d}`, testMaxUploadBytes+1), user("alice"), http.StatusBadRequest, "file size exceeds 1MB limit"},
		{"type not allowed", `{"filename":"a.exe","mime_type":"application/x-msdownload","size":10}`, user("alice"), http.StatusUnsupportedMediaType, "invalid or prohibited file type: application/x-msdownload"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := upload(h, tt.p, tt.body)
			assertStatus(t, rec, tt.wantStatus)
			if !strings.Contains(rec.Body.String(), tt.wantMessage) {
				t.Errorf("body %s, want it to say %q", rec.Body.String(), tt.wantMessage)
			}
		})
	}

	t.Run("no reference can be issued", func(t *testing.T) {
		rec := upload(NewFileHandler(stack.Service, failingRefs{}), user("alice"), `{"filename":"a.pdf","mime_type":"application/pdf","size":10}`)
		assertStatus(t, rec, http.StatusInternalServerError)
	})
}

// Without WithStrictRequestBodies an upload request reads as it always has:
// unknown fields and trailing data are ignored, and any request that can't be
// read is a 400. With it, those are refused and a request over the limit is a
// 413.
func TestFileHandler_UploadRequestBody(t *testing.T) {
	stack, err := New(context.Background(), localConfig(t, "http://localhost:8080"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	access, _ := testFileAccess(t)
	lenient := NewFileHandler(stack.Service, access)
	strict := NewFileHandler(stack.Service, access, WithStrictRequestBodies())

	valid := `{"filename":"a.pdf","mime_type":"application/pdf","size":10}`
	tests := []struct {
		name                        string
		body                        string
		lenientStatus, strictStatus int
		strictMessage               string
	}{
		{"valid", valid, http.StatusOK, http.StatusOK, ""},
		{"unknown field", `{"filename":"a.pdf","mime_type":"application/pdf","size":10,"key":"k"}`, http.StatusOK, http.StatusBadRequest, `invalid request body: unknown field \"key\"`},
		{"trailing data", valid + `{}`, http.StatusOK, http.StatusBadRequest, "invalid request body"},
		{"over the limit", `{"filename":"` + strings.Repeat("a", maxUploadRequestBytes) + `.pdf"}`, http.StatusBadRequest, http.StatusRequestEntityTooLarge, "request body too large"},
		{"empty", ``, http.StatusBadRequest, http.StatusBadRequest, "invalid request body"},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/lenient", func(t *testing.T) {
			rec := upload(lenient, user("alice"), tt.body)
			assertStatus(t, rec, tt.lenientStatus)
			if tt.lenientStatus == http.StatusBadRequest && !strings.Contains(rec.Body.String(), "invalid request body") {
				t.Errorf("body %s, want it to say %q", rec.Body.String(), "invalid request body")
			}
		})
		t.Run(tt.name+"/strict", func(t *testing.T) {
			rec := upload(strict, user("alice"), tt.body)
			assertStatus(t, rec, tt.strictStatus)
			if !strings.Contains(rec.Body.String(), tt.strictMessage) {
				t.Errorf("body %s, want it to say %q", rec.Body.String(), tt.strictMessage)
			}
		})
	}
}

// In proxy mode the owning service's rejection is relayed, and its failure to
// accept this service's credentials is a gateway failure.
func TestFileHandler_UploadThroughProxy(t *testing.T) {
	access, _ := testFileAccess(t)

	h := NewFileHandler(newProxyStack(t, ownerToken).Service, access)
	assertStatus(t, upload(h, user("alice"), `{"filename":"a.exe","mime_type":"application/x-msdownload","size":10}`), http.StatusUnsupportedMediaType)

	h = NewFileHandler(newProxyStack(t, "wrong-token").Service, access)
	assertStatus(t, upload(h, user("alice"), `{"filename":"a.pdf","mime_type":"application/pdf","size":10}`), http.StatusBadGateway)
}

// Every upload route matches the file name's extension to its type: the user
// route and the internal route on a deployment that stores files, and in proxy
// mode the owning service, whose refusal is relayed.
func TestFileHandler_UploadExtension(t *testing.T) {
	const mismatch = `{"filename":"setup.exe","mime_type":"application/pdf","size":10}`
	const want = `the file name's extension (".exe") does not match its type (application/pdf)`

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	stack, err := New(context.Background(), localConfig(t, srv.URL), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stack.LocalContent.RegisterRoutes(mux)
	access, _ := testFileAccess(t)
	h := NewFileHandler(stack.Service, access, WithUploads(stack.Uploads))

	// errorOf returns the message of the error rec answered with.
	errorOf := func(rec *httptest.ResponseRecorder) string {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		return e.Error
	}

	rec := upload(h, user("alice"), mismatch)
	assertStatus(t, rec, http.StatusBadRequest)
	if got := errorOf(rec); got != want {
		t.Errorf("error %q, want %q", got, want)
	}

	// A name without an extension is stored, and named, with its type's.
	rec = upload(h, user("alice"), `{"filename":"scan","mime_type":"application/pdf","size":10}`)
	assertStatus(t, rec, http.StatusOK)
	var body struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	stored, err := access.Resolve(user("alice"), body.Key, fileaccess.ScopeSubmit)
	if err != nil {
		t.Fatal(err)
	}
	if body.Name != "scan.pdf" || !strings.HasSuffix(stored, ".pdf") {
		t.Errorf("named %q and stored as %q, want both to end in .pdf", body.Name, stored)
	}

	_, machine, _ := machineStack(t)
	assertStatus(t, internalUpload(machine, client(htmlClient), `{"filename":"doc.pdf","mime_type":"text/html; charset=utf-8","size":10}`), http.StatusBadRequest)
	assertStatus(t, internalUpload(machine, client(htmlClient), `{"filename":"doc.html","mime_type":"text/html; charset=utf-8","size":10}`), http.StatusOK)

	proxy := newProxyStack(t, ownerToken)
	rec = upload(NewFileHandler(proxy.Service, access, WithUploads(proxy.Uploads)), user("alice"), mismatch)
	assertStatus(t, rec, http.StatusBadRequest)
	if got := errorOf(rec); got != want {
		t.Errorf("proxied error %q, want it to relay %q", got, want)
	}
}

// An owning service that answers 200 with a response this service can't use
// failed, as surely as one that answered 5xx: a gateway failure, not this
// service's own (500).
func TestFileHandler_DownloadOwnerBadResponseIsBadGateway(t *testing.T) {
	access, _ := testFileAccess(t)
	for name, body := range map[string]string{
		"no download_url":       `{"expires_at": 1}`,
		"relative download_url": `{"download_url": "/api/v1/storage/k/content", "expires_at": 1}`,
	} {
		t.Run(name, func(t *testing.T) {
			owner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(owner.Close)
			svc, err := NewProxyService(newRegistry(t, owner.URL, ownerToken), defaultProxyConfig())
			if err != nil {
				t.Fatal(err)
			}
			token, err := access.Issue(user("alice"), "any-key", fileaccess.ScopeRead)
			if err != nil {
				t.Fatal(err)
			}
			assertStatus(t, download(NewFileHandler(svc, access), token, user("alice")), http.StatusBadGateway)
		})
	}
}

// A backend of this deployment's own failing is this service's error (500),
// not a gateway's.
func TestFileHandler_DownloadBackendFailureIsInternal(t *testing.T) {
	access, _ := testFileAccess(t)
	token, err := access.Issue(user("alice"), "any-key.pdf", fileaccess.ScopeRead)
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, download(NewFileHandler(failingService{}, access), token, user("alice")), http.StatusInternalServerError)
}

// failingService is a Service whose backend fails every call.
type failingService struct{ Service }

func (failingService) DownloadURL(context.Context, string) (corestorage.Download, error) {
	return corestorage.Download{}, errors.New("backend unavailable")
}

// A download says what storage says: the type the file was stored with and how
// its URL is served. The key's extension only names a file the client saves,
// so a filename that disagrees with the type changes neither.
func TestDownloadOf_ReportsWhatStorageSays(t *testing.T) {
	stack, err := New(context.Background(), localConfig(t, "http://localhost:8080"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tc := range []struct {
		name, filename, mimeType string
		wantExt, wantDisposition string
	}{
		{name: "a PDF", filename: "a.pdf", mimeType: "application/pdf", wantExt: ".pdf", wantDisposition: drivers.DispositionInline},
		{name: "XML", filename: "a.xml", mimeType: "text/xml", wantExt: ".xml", wantDisposition: drivers.DispositionAttachment},
		{name: "generated HTML", filename: "a.HTML", mimeType: "text/html; charset=utf-8", wantExt: ".HTML", wantDisposition: drivers.DispositionAttachment},
		{name: "XML named like a PDF", filename: "draft.pdf", mimeType: "text/xml", wantExt: ".pdf", wantDisposition: drivers.DispositionAttachment},
		{name: "a PDF named like XML", filename: "report.xml", mimeType: "application/pdf", wantExt: ".xml", wantDisposition: drivers.DispositionInline},
		{name: "an extension nothing maps to a type", filename: "a.unknownext", mimeType: "image/png", wantExt: ".unknownext", wantDisposition: drivers.DispositionInline},
		{name: "no extension", filename: "a", mimeType: "image/png", wantExt: "", wantDisposition: drivers.DispositionInline},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta, err := stack.Service.Save(context.Background(), tc.filename, tc.mimeType, strings.NewReader("data"), 4)
			if err != nil {
				t.Fatalf("Save: %v", err)
			}
			dl, err := downloadOf(context.Background(), stack.Service, meta.Key)
			if err != nil {
				t.Fatalf("downloadOf: %v", err)
			}
			if dl.Extension != tc.wantExt || dl.MimeType != tc.mimeType || dl.Disposition != tc.wantDisposition {
				t.Errorf("extension, mime_type, disposition = %q, %q, %q; want %q, %q, %q",
					dl.Extension, dl.MimeType, dl.Disposition, tc.wantExt, tc.mimeType, tc.wantDisposition)
			}
		})
	}
}
