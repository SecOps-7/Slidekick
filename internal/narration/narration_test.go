package narration

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SecOps-7/slidekick/internal/secret"
	"github.com/SecOps-7/slidekick/internal/store"
	"github.com/SecOps-7/slidekick/internal/tts"
)

type fakeEngine struct {
	mu   sync.Mutex
	kind string
	key  string
	reqs []tts.Request
}

func (f *fakeEngine) Synthesize(_ context.Context, r tts.Request) (*tts.Audio, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	f.mu.Unlock()
	return &tts.Audio{Data: []byte("mp3:" + r.Voice + ":" + r.Text), MIME: "audio/mpeg", Duration: time.Second}, nil
}

func (f *fakeEngine) Voices(context.Context) ([]tts.Voice, error) {
	return []tts.Voice{{ID: "v1", Name: "One"}}, nil
}

func newSvc(t *testing.T) (*Service, *fakeEngine) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _, err := secret.Load("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fe := &fakeEngine{}
	s := &Service{Store: st, Box: box, Engine: func(kind, base, key string) (tts.Engine, error) {
		fe.kind, fe.key = kind, key
		return fe, nil
	}}
	return s, fe
}

func TestSpeakDefaultsAndCache(t *testing.T) {
	s, fe := newSvc(t)
	ctx := context.Background()
	// Nothing configured: the browser speaks.
	if _, err := s.Speak(ctx, "", "", "Hello"); !errors.Is(err, tts.ErrBrowserVoice) {
		t.Fatalf("default is browser: %v", err)
	}
	key := "secret-key-123456"
	if _, err := s.SaveProvider(ctx, ProviderInput{ID: "eleven", Kind: "elevenlabs", APIKey: &key, DefaultVoice: "pv", DefaultModel: "m1"}); err != nil {
		t.Fatal(err)
	}
	// Explicit provider, its own default voice.
	a, err := s.Speak(ctx, "eleven", "", "Hello")
	if err != nil || string(a.Data) != "mp3:pv:Hello" || fe.key != key || fe.kind != "elevenlabs" || fe.reqs[0].Model != "m1" {
		t.Fatalf("speak: %v %q %+v", err, a.Data, fe.reqs)
	}
	// The instance default (provider + voice) applies to "".
	if err := s.SetDefault(ctx, "eleven", "dv"); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Speak(ctx, "", "", "Hello")
	if string(a.Data) != "mp3:dv:Hello" {
		t.Fatalf("default voice: %q", a.Data)
	}
	// Cached: same request, no new synthesis.
	n := len(fe.reqs)
	b, _ := s.Speak(ctx, "", "", "Hello")
	if b != a || len(fe.reqs) != n {
		t.Fatal("not cached")
	}
	url := s.AudioURL(a)
	hash := strings.TrimSuffix(strings.TrimPrefix(url, "/api/tts/audio/"), ".mp3")
	if len(hash) != 64 || !strings.HasSuffix(url, ".mp3") {
		t.Fatalf("url %q", url)
	}
	if got, ok := s.Cached(hash); !ok || got != a {
		t.Fatal("cached audio not found by URL hash")
	}
	// Removing the default provider goes back to the browser.
	if err := s.DeleteProvider(ctx, "eleven"); err != nil {
		t.Fatal(err)
	}
	if d := s.Default(ctx); d.Provider != BrowserID || d.Voice != "" {
		t.Fatalf("default after delete: %+v", d)
	}
	if _, err := s.Speak(ctx, BrowserID, "", "Hi"); !errors.Is(err, tts.ErrBrowserVoice) {
		t.Fatalf("browser: %v", err)
	}
}

func TestSpeakSplitsLongText(t *testing.T) {
	s, fe := newSvc(t)
	ctx := context.Background()
	key := "k-123456789"
	s.SaveProvider(ctx, ProviderInput{ID: "dg", Kind: "deepgram", APIKey: &key})
	text := strings.Repeat("This is one sentence of the notes. ", 150) // ~5000 characters
	a, err := s.Speak(ctx, "dg", "aura-2-zeus-en", text)
	if err != nil {
		t.Fatal(err)
	}
	if len(fe.reqs) != 3 || a.Duration != 3*time.Second {
		t.Fatalf("%d parts, %v", len(fe.reqs), a.Duration)
	}
	for _, r := range fe.reqs {
		if len(r.Text) > tts.Limit(tts.KindDeepgram) || !strings.HasSuffix(r.Text, ".") {
			t.Fatalf("part of %d chars ending %q", len(r.Text), r.Text[len(r.Text)-5:])
		}
	}
	if _, err := s.Speak(ctx, "dg", "", strings.Repeat("a", MaxText+1)); !errors.Is(err, ErrValidation) {
		t.Fatalf("too long: %v", err)
	}
}

func TestProvidersAndValidation(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	if _, err := s.SaveProvider(ctx, ProviderInput{ID: "browser", Kind: "openai"}); !errors.Is(err, ErrValidation) {
		t.Fatal("browser id accepted")
	}
	if _, err := s.SaveProvider(ctx, ProviderInput{ID: "x", Kind: "openai-compatible"}); !errors.Is(err, ErrValidation) {
		t.Fatal("compatible without URL accepted")
	}
	if _, err := s.SaveProvider(ctx, ProviderInput{ID: "x", Kind: "openai-compatible", BaseURL: "ftp://x"}); !errors.Is(err, ErrValidation) {
		t.Fatal("ftp URL accepted")
	}
	if _, err := s.SaveProvider(ctx, ProviderInput{ID: "kokoro", Name: "Kokoro", Kind: "openai-compatible", BaseURL: "http://172.16.5.9:8880/v1/"}); err != nil {
		t.Fatal(err)
	}
	ps, d, err := s.Providers(ctx)
	if err != nil || len(ps) != 2 || ps[0].ID != BrowserID || !ps[0].BuiltIn || !ps[0].IsDefault || ps[1].BaseURL != "http://172.16.5.9:8880/v1" || d.Provider != BrowserID {
		t.Fatalf("%v %+v %+v", err, ps, d)
	}
	if err := s.SetDefault(ctx, "missing", ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown default: %v", err)
	}
	if err := s.DeleteProvider(ctx, BrowserID); !errors.Is(err, ErrValidation) {
		t.Fatal("browser removable")
	}
	vs, err := s.Voices(ctx, BrowserID)
	if err != nil || len(vs) != 0 {
		t.Fatalf("browser voices: %v %v", vs, err)
	}
}

func TestSplit(t *testing.T) {
	if p := Split("short", 10); len(p) != 1 || p[0] != "short" {
		t.Fatal(p)
	}
	p := Split("aaaa bbbb cccc dddd", 10)
	if len(p) != 2 || p[0] != "aaaa bbbb" {
		t.Fatalf("%q", p)
	}
	p = Split(strings.Repeat("x", 25), 10)
	if len(p) != 3 || len(p[2]) != 5 {
		t.Fatalf("%q", p)
	}
}

func TestCacheEviction(t *testing.T) {
	c := NewCache(10, time.Minute)
	now := time.Now()
	c.now = func() time.Time { return now }
	a := &tts.Audio{Data: []byte("123456")}
	b := &tts.Audio{Data: []byte("abcdef")}
	c.Put("a", a)
	c.Put("b", b) // over 10 bytes: a goes
	if _, ok := c.Get("a"); ok {
		t.Fatal("a kept over the size limit")
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("b dropped")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := c.Get("b"); ok {
		t.Fatal("expired entry served")
	}
	if c.Key("p", "v", "m", "t") == c.Key("p", "v", "mt", "") {
		t.Fatal("ambiguous key")
	}
	if NewCache(10, time.Minute).Key("p", "v", "m", "t") == c.Key("p", "v", "m", "t") {
		t.Fatal("keys guessable across instances")
	}
}
