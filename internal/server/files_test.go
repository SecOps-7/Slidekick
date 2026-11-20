package server_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SecOps-7/slidekick"
	"github.com/SecOps-7/slidekick/internal/blob"
	"github.com/SecOps-7/slidekick/internal/mcpserver"
	"github.com/SecOps-7/slidekick/internal/server"
	"github.com/SecOps-7/slidekick/internal/testenv"
)

// TestServeFileHeaders checks /files/{name} serves from the blob store with
// length, ETag, conditional and Range support, plus the sandbox headers.
func TestServeFileHeaders(t *testing.T) {
	env := testenv.New(t)
	env.Svc.FilesDir = filepath.Join(t.TempDir(), "files")
	testServeFile(t, env)
}

// TestServeFileS3 is the same check with files in a bucket; it is skipped
// unless SLIDEKICK_TEST_S3_* is set (see testenv.S3Config).
func TestServeFileS3(t *testing.T) {
	cfg := testenv.S3Config(t)
	env := testenv.New(t)
	bs, err := blob.NewS3(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	env.Svc.Blobs = bs
	testServeFile(t, env)
}

func testServeFile(t *testing.T, env *testenv.Env) {
	h := server.New(env.Svc, mcpserver.New(env.Svc, slidekick.Schema, "test", mcpserver.Options{}),
		server.Options{Token: token, SchemaFS: slidekick.Schema, Web: fstest.MapFS{"index.html": {Data: []byte("app")}}})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	data := img.Bytes()
	a, err := env.Svc.UploadAsset(context.Background(), "x.png", "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	pdf, err := env.Svc.SaveGenerated(context.Background(), "deck.pdf", "application/pdf", []byte("%PDF-1.4 test"))
	if err != nil {
		t.Fatal(err)
	}

	get := func(path string, hdr map[string]string) (*http.Response, []byte) {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, b
	}

	res, body := get(a.URL(), nil)
	if res.StatusCode != 200 || !bytes.Equal(body, data) {
		t.Fatalf("GET %s: %d, %d bytes", a.URL(), res.StatusCode, len(body))
	}
	etag := `"` + a.SHA256 + `"`
	want := map[string]string{
		"Content-Type":   "image/png",
		"Content-Length": strconv.Itoa(len(data)),
		"ETag":           etag,
		"Accept-Ranges":  "bytes",
		"Cache-Control":  "public, max-age=31536000, immutable",
	}
	for k, v := range want {
		if got := res.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("CSP %q should sandbox", csp)
	}
	if res.Header.Get("Content-Disposition") != "" {
		t.Error("images should be served inline")
	}

	if res, _ := get(a.URL(), map[string]string{"If-None-Match": etag}); res.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match: %d, want 304", res.StatusCode)
	}
	res, body = get(a.URL(), map[string]string{"Range": "bytes=1-3"})
	if res.StatusCode != http.StatusPartialContent || !bytes.Equal(body, data[1:4]) ||
		res.Header.Get("Content-Range") != "bytes 1-3/"+strconv.Itoa(len(data)) {
		t.Errorf("Range: %d %q %q", res.StatusCode, body, res.Header.Get("Content-Range"))
	}

	res, _ = get(pdf.URL(), nil)
	if cd := res.Header.Get("Content-Disposition"); res.StatusCode != 200 || cd != `attachment; filename="deck.pdf"` {
		t.Errorf("pdf: %d %q", res.StatusCode, cd)
	}

	if res, _ := get("/files/f_00000000000000000000.png", nil); res.StatusCode != 404 {
		t.Errorf("unknown file: %d, want 404", res.StatusCode)
	}
}
