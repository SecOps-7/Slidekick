package service_test

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SecOps-7/slidekick/internal/testenv"
)

func tinyPNG(t *testing.T) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestUploadAsset(t *testing.T) {
	env := testenv.New(t)
	env.Svc.FilesDir = filepath.Join(t.TempDir(), "files")
	data := tinyPNG(t)

	a, err := env.Svc.UploadAsset(ctx, "../../etc/passwd.png", "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a.URL(), "/files/f_") || !strings.HasSuffix(a.URL(), ".png") || a.Name != "passwd.png" {
		t.Fatalf("bad asset %+v", a)
	}
	b, err := env.Svc.UploadAsset(ctx, "again.png", "image/png", data)
	if err != nil || b.ID != a.ID {
		t.Fatalf("identical bytes should dedupe: %v %v", b, err)
	}
	_, r, err := env.Svc.OpenAsset(ctx, a.ID+a.Ext)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	if !bytes.Equal(got, data) {
		t.Fatal("stored bytes differ")
	}
	if _, err := os.Stat(filepath.Join(env.Svc.FilesDir, a.ID+a.Ext)); err != nil {
		t.Fatalf("disk backend should write under FilesDir: %v", err)
	}
	if _, b, err := env.Svc.ReadAsset(ctx, a.ID); err != nil || !bytes.Equal(b, data) {
		t.Fatalf("ReadAsset: %v", err)
	}

	bad := map[string]struct {
		mime string
		data []byte
	}{
		"wrong type":    {"image/jpeg", data},
		"not allowed":   {"application/pdf", []byte("%PDF-1.4")},
		"empty":         {"image/png", nil},
		"svg not svg":   {"image/svg+xml", []byte("<html>")},
		"html as video": {"video/mp4", []byte("<!doctype html><html></html>")},
	}
	for name, c := range bad {
		if _, err := env.Svc.UploadAsset(ctx, "x", c.mime, c.data); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	for _, id := range []string{"../slidekick.db", "f_123", "f_zzzzzzzzzzzzzzzzzzzz.png"} {
		if _, _, err := env.Svc.OpenAsset(ctx, id); err == nil {
			t.Errorf("OpenAsset(%q) should fail", id)
		}
	}
}

func TestShowcaseDeckIsValid(t *testing.T) {
	env := testenv.New(t)
	d, err := env.Validator.Validate(testenv.Example(t, "showcase.json"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range d.Slides {
		for _, e := range s.Elements(env.Cat) {
			seen[e.Type] = true
		}
	}
	for _, typ := range []string{"chart", "diagram", "icon", "shape", "code", "stat"} {
		if !seen[typ] {
			t.Errorf("showcase should use %s", typ)
		}
	}
}

func TestUnknownIconRejected(t *testing.T) {
	env := testenv.New(t)
	_, err := env.Svc.Create(ctx, "Icons", "", "", []any{map[string]any{
		"layout": "title", "regions": map[string]any{"title": []any{map[string]any{"type": "icon", "props": map[string]any{"name": "not-an-icon"}}}},
	}}, "agent")
	if err == nil || !strings.Contains(err.Error(), "unknown icon") {
		t.Fatalf("want unknown icon error, got %v", err)
	}
}
