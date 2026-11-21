package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SecOps-7/slidekick"
	"github.com/SecOps-7/slidekick/internal/accounts"
	"github.com/SecOps-7/slidekick/internal/mcpserver"
	"github.com/SecOps-7/slidekick/internal/narration"
	"github.com/SecOps-7/slidekick/internal/oidc"
	"github.com/SecOps-7/slidekick/internal/secret"
	"github.com/SecOps-7/slidekick/internal/server"
	"github.com/SecOps-7/slidekick/internal/testenv"
	"github.com/SecOps-7/slidekick/internal/tts"
)

const ttsKey = "tts-test-super-secret-key-0987654321"

type fakeTTS struct {
	mu   sync.Mutex
	keys []string
	reqs []tts.Request
}

func (f *fakeTTS) Synthesize(_ context.Context, r tts.Request) (*tts.Audio, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	f.mu.Unlock()
	return &tts.Audio{Data: []byte("ID3fake-mp3:" + r.Voice + ":" + r.Text), MIME: "audio/mpeg", Duration: 1500 * time.Millisecond}, nil
}

func (f *fakeTTS) Voices(context.Context) ([]tts.Voice, error) {
	return []tts.Voice{{ID: "v-1", Name: "Voice one", Lang: "en"}}, nil
}

type ttsEnv struct {
	*accEnv
	svc  *narration.Service
	fake *fakeTTS
}

func newTTSServer(t *testing.T) *ttsEnv {
	t.Helper()
	env := testenv.New(t)
	box, _, _ := secret.Load("", t.TempDir())
	acc := accounts.New(env.Store, box, oidc.NewManager(nil))
	f := &fakeTTS{}
	svc := &narration.Service{Store: env.Store, Box: box, Engine: func(kind, base, key string) (tts.Engine, error) {
		f.mu.Lock()
		f.keys = append(f.keys, key)
		f.mu.Unlock()
		return f, nil
	}}
	e := &ttsEnv{accEnv: &accEnv{env: env, acc: acc}, svc: svc, fake: f}
	mcpSrv := mcpserver.New(env.Svc, slidekick.Schema, "test", mcpserver.Options{Policy: acc.Policy()})
	e.ts = httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(e.ts.Close)
	e.ts.Config.Handler = server.New(env.Svc, mcpSrv, server.Options{BaseURL: e.ts.URL, Accounts: acc, TTS: svc, SchemaFS: slidekick.Schema,
		Web: fstest.MapFS{"index.html": {Data: []byte("app")}}})
	return e
}

// raw sends a request and returns status, headers and body bytes.
func (c *client) raw(method, path string, body any) (int, http.Header, []byte) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, b
}

func TestTTSAdminOnlyAndKeyNeverEchoed(t *testing.T) {
	e := newTTSServer(t)
	admin := e.withAdmin(t)
	code, out := admin.do("PUT", "/api/admin/tts/eleven", map[string]any{"name": "ElevenLabs", "kind": "elevenlabs", "apiKey": ttsKey, "defaultVoice": "v-1"})
	if code != 200 || strings.Contains(toJSON(out), ttsKey) || out["hasKey"] != true {
		t.Fatalf("save: %d %s", code, toJSON(out))
	}
	_, list := admin.do("GET", "/api/admin/tts", nil)
	s := toJSON(list)
	if strings.Contains(s, ttsKey) || !strings.Contains(s, `"hasKey":true`) || !strings.Contains(s, `"id":"browser"`) || !strings.Contains(s, `"builtIn":true`) {
		t.Fatalf("admin list: %s", s)
	}
	p, _ := e.env.Store.GetTTSProvider(context.Background(), "eleven")
	if p.KeyEnc == "" || strings.Contains(p.KeyEnc, ttsKey) {
		t.Fatalf("key stored in the clear: %q", p.KeyEnc)
	}
	// Saving without a key keeps it.
	admin.do("PUT", "/api/admin/tts/eleven", map[string]any{"name": "ElevenLabs 2", "kind": "elevenlabs"})
	if p2, _ := e.env.Store.GetTTSProvider(context.Background(), "eleven"); p2.KeyEnc == "" {
		t.Fatal("key lost when saving without one")
	}

	ed := e.addUser(t, admin, "edna", "editor")
	view := e.addUser(t, admin, "vera", "viewer")
	for _, c := range []struct{ m, p string }{
		{"GET", "/api/admin/tts"}, {"PUT", "/api/admin/tts/x"}, {"DELETE", "/api/admin/tts/eleven"},
		{"POST", "/api/admin/tts/eleven/default"}, {"POST", "/api/admin/tts/eleven/test"},
	} {
		if code, _ := ed.do(c.m, c.p, map[string]any{"kind": "openai"}); code != 403 {
			t.Errorf("editor %s %s: %d", c.m, c.p, code)
		}
	}
	// Editors list providers and voices, without URLs or keys; viewers can't.
	_, pub := ed.do("GET", "/api/tts/providers", nil)
	if s := toJSON(pub); strings.Contains(s, ttsKey) || strings.Contains(s, "api.elevenlabs.io") || !strings.Contains(s, `"id":"eleven"`) {
		t.Fatalf("public providers: %s", s)
	}
	code, vs := ed.do("GET", "/api/tts/voices?provider=eleven", nil)
	if code != 200 || !strings.Contains(toJSON(vs), "v-1") {
		t.Fatalf("voices: %d %s", code, toJSON(vs))
	}
	if code, out := ed.do("GET", "/api/tts/voices?provider=browser", nil); code != 200 || out["browser"] != true {
		t.Fatalf("browser voices: %d %v", code, out)
	}
	if code, _ := view.do("GET", "/api/tts/voices?provider=eleven", nil); code != 403 {
		t.Fatalf("viewer voices: %d", code)
	}
	e.fake.mu.Lock()
	if len(e.fake.keys) == 0 || e.fake.keys[0] != ttsKey {
		t.Fatal("engine did not get the decrypted key")
	}
	e.fake.mu.Unlock()

	// Validation and removal.
	if code, _ := admin.do("PUT", "/api/admin/tts/bad", map[string]any{"kind": "openai-compatible", "baseUrl": "file:///etc/passwd"}); code != 422 {
		t.Fatalf("bad base URL: %d", code)
	}
	if code, _ := admin.do("DELETE", "/api/admin/tts/browser", nil); code != 422 {
		t.Fatalf("delete browser: %d", code)
	}
	if code, _ := admin.do("DELETE", "/api/admin/tts/eleven", nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
}

func TestTTSDefaultSampleAndAudioURL(t *testing.T) {
	e := newTTSServer(t)
	admin := e.withAdmin(t)
	admin.do("PUT", "/api/admin/tts/kokoro", map[string]any{"name": "Kokoro", "kind": "openai-compatible", "baseUrl": "http://172.16.5.9:8880/v1", "defaultModel": "kokoro"})

	// Play sample returns audio.
	code, h, body := admin.raw("POST", "/api/admin/tts/kokoro/test", map[string]any{"voice": "af_bella"})
	if code != 200 || h.Get("Content-Type") != "audio/mpeg" || !strings.HasPrefix(string(body), "ID3fake-mp3:af_bella:") || h.Get("X-Audio-Duration-Ms") != "1500" {
		t.Fatalf("sample: %d %v %q", code, h, body)
	}
	if code, _, _ := admin.raw("POST", "/api/admin/tts/browser/test", map[string]any{}); code != 409 {
		t.Fatalf("browser sample: %d", code)
	}

	// The default is the browser until set.
	if _, err := e.svc.Speak(context.Background(), "", "", "Hi"); err != tts.ErrBrowserVoice {
		t.Fatalf("browser default: %v", err)
	}
	if code, out := admin.do("POST", "/api/admin/tts/kokoro/default", map[string]any{"voice": "am_adam"}); code != 200 || out["provider"] != "kokoro" {
		t.Fatalf("set default: %d %v", code, out)
	}
	_, list := admin.do("GET", "/api/admin/tts", nil)
	if !strings.Contains(toJSON(list), `"default":{"provider":"kokoro","voice":"am_adam"}`) {
		t.Fatalf("list default: %s", toJSON(list))
	}

	a, err := e.svc.Speak(context.Background(), "", "", "Welcome to the quarterly review.")
	if err != nil {
		t.Fatal(err)
	}
	url := e.svc.AudioURL(a)
	if !strings.HasPrefix(url, "/api/tts/audio/") || !strings.HasSuffix(url, ".mp3") {
		t.Fatalf("url %q", url)
	}
	// Served without credentials (cast receivers), private caching, ranges work.
	anon := e.client(t)
	code, h, body = anon.raw("GET", url, nil)
	if code != 200 || string(body) != "ID3fake-mp3:am_adam:Welcome to the quarterly review." || h.Get("Content-Type") != "audio/mpeg" || !strings.HasPrefix(h.Get("Cache-Control"), "private") {
		t.Fatalf("audio: %d %v %q", code, h, body)
	}
	req, _ := http.NewRequest("GET", e.ts.URL+url, nil)
	req.Header.Set("Range", "bytes=0-2")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 206 || string(rb) != "ID3" {
		t.Fatalf("range: %d %q", res.StatusCode, rb)
	}
	for _, bad := range []string{"/api/tts/audio/" + strings.Repeat("0", 64) + ".mp3", "/api/tts/audio/nothex.mp3", strings.TrimSuffix(url, ".mp3")} {
		if code, _, _ := anon.raw("GET", bad, nil); code != 404 {
			t.Errorf("%s: %d", bad, code)
		}
	}
	// Removing the default provider falls back to the browser.
	admin.do("DELETE", "/api/admin/tts/kokoro", nil)
	if d := e.svc.Default(context.Background()); d.Provider != narration.BrowserID {
		t.Fatalf("default after delete: %+v", d)
	}
}
