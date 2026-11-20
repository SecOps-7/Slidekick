package blob_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/SecOps-7/slidekick/internal/blob"
)

func TestDisk(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "files")
	testStore(t, blob.NewDisk(dir))
	st, err := os.Stat(dir)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("files dir should be 0700: %v %v", st, err)
	}
	d := blob.NewDisk(dir)
	for _, k := range []string{"../x", "a/b", ".upload-1", ""} {
		if err := d.Put(context.Background(), k, "", []byte("x")); err == nil {
			t.Errorf("Put(%q) should fail", k)
		}
		if _, _, err := d.Open(context.Background(), k); !errors.Is(err, blob.ErrNotFound) {
			t.Errorf("Open(%q) = %v, want ErrNotFound", k, err)
		}
	}
}

// testStore exercises the Store contract; the S3 integration test reuses it.
func testStore(t *testing.T, s blob.Store) {
	t.Helper()
	ctx := context.Background()
	key := "f_0123456789abcdef0123.txt"
	data := []byte("hello, blob store")

	if ok, err := s.Exists(ctx, key); err != nil || ok {
		t.Fatalf("Exists before Put = %v, %v", ok, err)
	}
	if _, _, err := s.Open(ctx, key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Open missing = %v, want ErrNotFound", err)
	}
	if err := s.Put(ctx, key, "text/plain", data); err != nil {
		t.Fatal(err)
	}
	// Content-addressed: a second Put of the same key is a no-op.
	if err := s.Put(ctx, key, "text/plain", []byte("other")); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Exists(ctx, key); err != nil || !ok {
		t.Fatalf("Exists after Put = %v, %v", ok, err)
	}
	r, info, err := s.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if info.Size != int64(len(data)) {
		t.Fatalf("size %d, want %d", info.Size, len(data))
	}
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read %q, %v", got, err)
	}
	if _, err := r.Seek(7, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	part := make([]byte, 5)
	if _, err := io.ReadFull(r, part); err != nil || string(part) != "blob " {
		t.Fatalf("seek+read %q, %v", part, err)
	}
	if b, err := blob.ReadAll(ctx, s, key); err != nil || !bytes.Equal(b, data) {
		t.Fatalf("ReadAll %q, %v", b, err)
	}
}
