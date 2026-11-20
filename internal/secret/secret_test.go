package secret

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealOpen(t *testing.T) {
	dir := t.TempDir()
	b, where, err := Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	if where != filepath.Join(dir, "secret.key") {
		t.Fatalf("key from %q", where)
	}
	st, _ := os.Stat(where)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v", st.Mode().Perm())
	}
	s, err := b.Seal("sk-live-abcdef123456", "ai:openai")
	if err != nil || strings.Contains(s, "abcdef") || !strings.HasPrefix(s, "v1:") {
		t.Fatalf("sealed %q, %v", s, err)
	}
	if got, err := b.Open(s, "ai:openai"); err != nil || got != "sk-live-abcdef123456" {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := b.Open(s, "ai:other"); err == nil {
		t.Fatal("opened under another purpose")
	}
	// Reloading uses the same key file.
	b2, _, _ := Load("", dir)
	if got, _ := b2.Open(s, "ai:openai"); got != "sk-live-abcdef123456" {
		t.Fatal("key file not reused")
	}
	// A different key can't read it.
	b3, _, err := Load(strings.Repeat("ab", 32), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b3.Open(s, "ai:openai"); err == nil {
		t.Fatal("opened with another key")
	}
	if _, _, err := Load("short", ""); err == nil {
		t.Fatal("accepted a short key")
	}
}
