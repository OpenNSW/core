package tasktypes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/OpenNSW/core/remote"
	"github.com/OpenNSW/core/taskflow/plugins"
	"github.com/OpenNSW/core/taskflow/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFetcher answers every download with one response, and records the request.
type fakeFetcher struct {
	resp      *remote.RawResponse
	err       error
	calls     int
	serviceID string
	req       remote.Request
}

func (f *fakeFetcher) CallRaw(_ context.Context, serviceID string, req remote.Request) (*remote.RawResponse, error) {
	f.calls++
	f.serviceID, f.req = serviceID, req
	return f.resp, f.err
}

func pdfResponse(status int, body string) *remote.RawResponse {
	return &remote.RawResponse{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
		Body:       []byte(body),
	}
}

const archiveConfig = `{"service_id": "port", "source_input": "payment_slip_url", "mime_type": "application/pdf", "filename": "payment-slip.pdf"}`

const slipURL = "https://files.example.com/pdf/payment-slip/26213643263442?signature=a8f1fc80"

func archiveCtx(source any) plugins.PluginContext {
	inputs := map[string]any{}
	if source != nil {
		inputs["payment_slip_url"] = source
	}
	return plugins.PluginContext{
		Context:         context.Background(),
		Inputs:          inputs,
		Record:          &store.TaskRecord{TaskID: "task-1", Data: map[string]any{}},
		OutputNamespace: "payment_slip_file",
	}
}

func TestRemoteFileArchiver_DownloadsAndStoresTheSource(t *testing.T) {
	fetcher := &fakeFetcher{resp: pdfResponse(http.StatusOK, "%PDF-1.7 slip")}
	files := &fakeSaver{}
	p := NewRemoteFileArchiverPlugin(fetcher, files)
	ctx := archiveCtx(slipURL)

	require.NoError(t, p.Execute(ctx, json.RawMessage(archiveConfig)))

	assert.Equal(t, "port", fetcher.serviceID, "the download goes through the service's own client")
	assert.Equal(t, http.MethodGet, fetcher.req.Method)
	assert.Equal(t, slipURL, fetcher.req.Path, "the signed link is fetched as sent")
	assert.Equal(t, "%PDF-1.7 slip", string(files.content))
	assert.Equal(t, "payment-slip.pdf", files.filename)
	assert.Equal(t, "application/pdf", files.mime, "stored as the configured type, whatever the service labels it")
	assert.Equal(t, map[string]any{
		"key":       "0f8e7c1a-0000-4000-8000-000000000001.html",
		"name":      "payment-slip.pdf",
		"mime_type": "application/pdf",
		"size":      int64(len("%PDF-1.7 slip")),
	}, ctx.Record.Data["payment_slip_file"])
}

const barcodeConfig = `{"service_id": "port", "source_input": "payment_slip_url", "mime_type": "image/png", "filename": "gate-pass-barcode.png"}`

func TestRemoteFileArchiver_DecodesAnInlineSource(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nbarcode")
	for name, source := range map[string]string{
		"inline":                 "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		"in another letter case": "DATA:Image/PNG;BASE64," + base64.StdEncoding.EncodeToString(png),
		"with a charset first":   "data:image/png;charset=utf-8;base64," + base64.StdEncoding.EncodeToString(png),
	} {
		t.Run(name, func(t *testing.T) {
			fetcher, files := &fakeFetcher{}, &fakeSaver{}
			ctx := archiveCtx(source)

			require.NoError(t, NewRemoteFileArchiverPlugin(fetcher, files).Execute(ctx, json.RawMessage(barcodeConfig)))

			assert.Zero(t, fetcher.calls, "an inline document is not downloaded")
			assert.Equal(t, png, files.content)
			assert.Equal(t, "gate-pass-barcode.png", files.filename)
			assert.Equal(t, "image/png", files.mime)
			assert.Equal(t, int64(len(png)), ctx.Record.Data["payment_slip_file"].(map[string]any)["size"])
		})
	}
}

func TestRemoteFileArchiver_RecordsAnInlineSourceItCannotStore(t *testing.T) {
	for name, tc := range map[string]struct{ source, want string }{
		"another type":    {"data:image/jpeg;base64,/9j/", `the inline document is "image/jpeg", not image/png`},
		"no comma":        {"data:image/png;base64", "the inline document is not a data: URI"},
		"not base64":      {"data:image/png,%89PNG", "the inline document is not base64-encoded"},
		"invalid base64":  {"data:image/png;base64,***", "the inline document is not valid base64"},
		"an empty source": {"data:image/png;base64,", "the inline document is empty"},
	} {
		t.Run(name, func(t *testing.T) {
			fetcher, files := &fakeFetcher{}, &fakeSaver{}
			ctx := archiveCtx(tc.source)

			require.NoError(t, NewRemoteFileArchiverPlugin(fetcher, files).Execute(ctx, json.RawMessage(barcodeConfig)),
				"a retry would decode the same URI")
			assert.Zero(t, fetcher.calls)
			assert.Zero(t, files.calls)
			assert.Equal(t, map[string]any{"error": tc.want}, ctx.Record.Data["payment_slip_file"])
		})
	}
}

func TestRemoteFileArchiver_StoresNothingWithoutASource(t *testing.T) {
	for name, source := range map[string]any{
		"no source":    nil,
		"empty":        "  ",
		"not a string": 42,
	} {
		t.Run(name, func(t *testing.T) {
			fetcher, files := &fakeFetcher{}, &fakeSaver{}
			ctx := archiveCtx(source)

			require.NoError(t, NewRemoteFileArchiverPlugin(fetcher, files).Execute(ctx, json.RawMessage(archiveConfig)))

			assert.Zero(t, fetcher.calls)
			assert.Zero(t, files.calls)
			assert.NotContains(t, ctx.Record.Data, "payment_slip_file", "the page keeps what the service sent")
		})
	}
}

func TestRemoteFileArchiver_RecordsARefusedDownloadAndGoesOn(t *testing.T) {
	for name, tc := range map[string]struct {
		resp *remote.RawResponse
		want string
	}{
		"an expired link": {pdfResponse(http.StatusForbidden, "expired"), "port refused the download with status 403"},
		"an empty answer": {pdfResponse(http.StatusOK, ""), "port sent an empty document"},
	} {
		t.Run(name, func(t *testing.T) {
			files := &fakeSaver{}
			ctx := archiveCtx(slipURL)

			err := NewRemoteFileArchiverPlugin(&fakeFetcher{resp: tc.resp}, files).Execute(ctx, json.RawMessage(archiveConfig))

			require.NoError(t, err, "the applicant still has the service's own link, and a retry would be refused again")
			assert.Zero(t, files.calls)
			assert.Equal(t, map[string]any{"error": tc.want}, ctx.Record.Data["payment_slip_file"])
		})
	}
}

func TestRemoteFileArchiver_ReturnsAFailureARetryCanFix(t *testing.T) {
	for name, fetcher := range map[string]*fakeFetcher{
		"transport": {err: errors.New("connection reset")},
		"5xx":       {resp: pdfResponse(http.StatusBadGateway, "")},
	} {
		t.Run(name, func(t *testing.T) {
			files := &fakeSaver{}
			ctx := archiveCtx(slipURL)

			err := NewRemoteFileArchiverPlugin(fetcher, files).Execute(ctx, json.RawMessage(archiveConfig))

			require.Error(t, err)
			assert.Zero(t, files.calls)
			assert.NotContains(t, ctx.Record.Data, "payment_slip_file")
		})
	}
}

func TestRemoteFileArchiver_ReturnsAStorageFailure(t *testing.T) {
	files := &fakeSaver{err: errors.New("bucket unavailable")}
	ctx := archiveCtx(slipURL)

	err := NewRemoteFileArchiverPlugin(&fakeFetcher{resp: pdfResponse(http.StatusOK, "%PDF")}, files).Execute(ctx, json.RawMessage(archiveConfig))

	require.ErrorContains(t, err, "bucket unavailable")
	assert.NotContains(t, ctx.Record.Data, "payment_slip_file")
}

func TestRemoteFileArchiver_RequiresItsConfig(t *testing.T) {
	p := NewRemoteFileArchiverPlugin(&fakeFetcher{}, &fakeSaver{})
	for _, field := range []string{"service_id", "source_input", "mime_type", "filename"} {
		t.Run(field, func(t *testing.T) {
			var cfg map[string]string
			require.NoError(t, json.Unmarshal([]byte(archiveConfig), &cfg))
			delete(cfg, field)
			raw, _ := json.Marshal(cfg)

			require.ErrorContains(t, p.Execute(archiveCtx(slipURL), raw), field)
		})
	}

	ctx := archiveCtx(slipURL)
	ctx.OutputNamespace = ""
	require.ErrorContains(t, p.Execute(ctx, json.RawMessage(archiveConfig)), "output_namespace")
}
