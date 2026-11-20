// Package testenv builds a fully wired deck service on a temp database for tests.
package testenv

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/SecOps-7/slidekick"
	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/hub"
	"github.com/SecOps-7/slidekick/internal/service"
	"github.com/SecOps-7/slidekick/internal/store"
)

type Env struct {
	Svc       *service.Service
	Cat       *deck.Catalog
	Themes    *deck.ThemeSet
	Validator *deck.Validator
	Store     *store.Store
}

func New(t testing.TB) *Env {
	t.Helper()
	cat, err := deck.LoadCatalog(slidekick.Schema)
	if err != nil {
		t.Fatal(err)
	}
	themes, err := deck.LoadThemes(slidekick.Themes)
	if err != nil {
		t.Fatal(err)
	}
	v, err := deck.NewValidator(slidekick.Schema, cat, themes)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Env{Svc: service.New(st, v, cat, themes, hub.New(), "http://test"), Cat: cat, Themes: themes, Validator: v, Store: st}
}

// Example reads a golden deck from /examples.
func Example(t testing.TB, name string) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "examples", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
