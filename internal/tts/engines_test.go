package tts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testKey = "test-key-0123456789abcdef"

type seen struct {
	method, path, query string
	header              http.Header
	body                map[string]any
}

// fake answers every request with reply and records what it saw.
func fake(t *testing.T, reply func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *[]seen) {
	t.Helper()
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := seen{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, header: r.Header.Clone()}
		b, _ := io.ReadAll(r.Body)
		if len(b) > 0 {
			json.Unmarshal(b, &s.body)
		}
		got = append(got, s)
		reply(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func mp3Reply(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Write(fakeMP3(50, true))
}

func synth(t *testing.T, kind, base, key string, req Request) *Audio {
	t.Helper()
	e, err := New(kind, base, key)
	if err != nil {
		t.Fatal(err)
	}
	a, err := e.Synthesize(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if a.MIME != "audio/mpeg" || a.Duration <= 0 || len(a.Data) == 0 {
		t.Fatalf("audio: %s %v %d", a.MIME, a.Duration, len(a.Data))
	}
	return a
}

func TestSynthesizeRequests(t *testing.T) {
	cases := []struct {
		kind, base string
		req        Request
		check      func(t *testing.T, s seen)
	}{
		{"fish", "", Request{Text: "Hello", Voice: "ref123", Model: "s1", Speed: 1.2}, func(t *testing.T, s seen) {
			want(t, s.method == "POST" && s.path == "/v1/tts", "path %s %s", s.method, s.path)
			want(t, s.header.Get("Authorization") == "Bearer "+testKey, "auth %q", s.header.Get("Authorization"))
			want(t, s.header.Get("model") == "s1", "model header %q", s.header.Get("model"))
			want(t, s.body["text"] == "Hello" && s.body["reference_id"] == "ref123" && s.body["format"] == "mp3", "body %v", s.body)
			want(t, s.body["prosody"].(map[string]any)["speed"] == 1.2, "speed %v", s.body["prosody"])
		}},
		{"elevenlabs", "", Request{Text: "Hello", Voice: "v/1"}, func(t *testing.T, s seen) {
			want(t, s.method == "POST" && s.path == "/v1/text-to-speech/v/1", "path %s", s.path)
			want(t, s.query == "output_format=mp3_44100_128", "query %s", s.query)
			want(t, s.header.Get("xi-api-key") == testKey && s.header.Get("Authorization") == "", "auth headers %v", s.header)
			want(t, s.body["text"] == "Hello" && s.body["model_id"] == "eleven_multilingual_v2", "body %v", s.body)
		}},
		{"openai", "", Request{Text: "Hello", Speed: 9}, func(t *testing.T, s seen) {
			want(t, s.method == "POST" && s.path == "/v1/audio/speech", "path %s", s.path)
			want(t, s.header.Get("Authorization") == "Bearer "+testKey, "auth")
			want(t, s.body["input"] == "Hello" && s.body["voice"] == "alloy" && s.body["model"] == "gpt-4o-mini-tts" && s.body["response_format"] == "mp3", "body %v", s.body)
			want(t, s.body["speed"] == 4.0, "speed clamped: %v", s.body["speed"])
		}},
		{"cartesia", "", Request{Text: "Hello", Voice: "c-voice"}, func(t *testing.T, s seen) {
			want(t, s.method == "POST" && s.path == "/tts/bytes", "path %s", s.path)
			want(t, s.header.Get("Authorization") == "Bearer "+testKey && s.header.Get("Cartesia-Version") == CartesiaVersion, "headers %v", s.header)
			want(t, s.body["transcript"] == "Hello" && s.body["model_id"] == "sonic-3.6", "body %v", s.body)
			want(t, s.body["voice"].(map[string]any)["id"] == "c-voice", "voice %v", s.body["voice"])
			of := s.body["output_format"].(map[string]any)
			want(t, of["container"] == "mp3" && of["sample_rate"] == 44100.0 && of["bit_rate"] == 128000.0, "format %v", of)
		}},
		{"deepgram", "", Request{Text: "Hello", Voice: "aura-2-zeus-en"}, func(t *testing.T, s seen) {
			want(t, s.method == "POST" && s.path == "/v1/speak", "path %s", s.path)
			want(t, strings.Contains(s.query, "model=aura-2-zeus-en") && strings.Contains(s.query, "encoding=mp3"), "query %s", s.query)
			want(t, s.header.Get("Authorization") == "Token "+testKey, "auth %q", s.header.Get("Authorization"))
			want(t, s.body["text"] == "Hello" && len(s.body) == 1, "body %v", s.body)
		}},
		{"openai-compatible", "/v1", Request{Text: "Hello", Voice: "af_bella", Model: "kokoro"}, func(t *testing.T, s seen) {
			want(t, s.path == "/v1/audio/speech", "path %s", s.path)
			want(t, s.header.Get("Authorization") == "", "no key, no auth header")
			want(t, s.body["voice"] == "af_bella" && s.body["model"] == "kokoro", "body %v", s.body)
		}},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			srv, got := fake(t, mp3Reply)
			key := testKey
			if c.kind == "openai-compatible" {
				key = ""
			}
			base := srv.URL + c.base
			if c.kind == "openai" {
				base = srv.URL + "/v1"
			}
			synth(t, c.kind, base, key, c.req)
			if len(*got) != 1 {
				t.Fatalf("%d requests", len(*got))
			}
			c.check(t, (*got)[0])
		})
	}
}

func want(t *testing.T, ok bool, f string, a ...any) {
	t.Helper()
	if !ok {
		t.Errorf(f, a...)
	}
}

func TestVoices(t *testing.T) {
	replies := map[string]string{
		"/model":           `{"total":1,"items":[{"_id":"f1","title":"Narrator","type":"tts","languages":["en"]}]}`,
		"/v2/voices":       `{"voices":[{"voice_id":"e1","name":"Rachel","labels":{"language":"en"}}],"has_more":false}`,
		"/voices":          `{"data":[{"id":"c1","name":"Katie","language":"en"}],"has_more":false}`,
		"/v1/models":       `{"stt":[],"tts":[{"name":"zeus","canonical_name":"aura-2-zeus-en","languages":["en","en-US"],"metadata":{"accent":"American"}}]}`,
		"/audio/voices":    `{"voices":["af_bella","am_adam"]}`,
		"/v1/audio/voices": `{"voices":[{"id":"af_sky","name":"Sky"}]}`,
	}
	srv, got := fake(t, func(w http.ResponseWriter, r *http.Request) {
		if b, ok := replies[r.URL.Path]; ok {
			w.Write([]byte(b))
			return
		}
		w.WriteHeader(404)
	})
	cases := []struct{ kind, base, wantID, wantPath, wantAuth string }{
		{"fish", srv.URL, "f1", "/model", "Bearer " + testKey},
		{"elevenlabs", srv.URL, "e1", "/v2/voices", ""},
		{"cartesia", srv.URL, "c1", "/voices", "Bearer " + testKey},
		{"deepgram", srv.URL, "aura-2-zeus-en", "/v1/models", "Token " + testKey},
		{"openai-compatible", srv.URL, "af_bella", "/audio/voices", ""},
		{"openai-compatible", srv.URL + "/v1", "af_sky", "/v1/audio/voices", ""},
		{"openai-compatible", srv.URL + "/nothing", "alloy", "/nothing/audio/voices", ""}, // 404: OpenAI names
		{"openai", srv.URL, "alloy", "", ""},                                              // static, no request
	}
	for _, c := range cases {
		*got = nil
		key := testKey
		if c.kind == "openai-compatible" {
			key = ""
		}
		e, err := New(c.kind, c.base, key)
		if err != nil {
			t.Fatal(err)
		}
		vs, err := e.Voices(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", c.kind, err)
		}
		if len(vs) == 0 || vs[0].ID != c.wantID {
			t.Errorf("%s: voices %+v", c.kind, vs)
		}
		if c.wantPath == "" {
			if len(*got) != 0 {
				t.Errorf("%s: unexpected request", c.kind)
			}
			continue
		}
		if len(*got) == 0 || (*got)[0].path != c.wantPath || (*got)[0].method != "GET" {
			t.Errorf("%s: requests %+v", c.kind, *got)
			continue
		}
		if c.wantAuth != "" && (*got)[0].header.Get("Authorization") != c.wantAuth {
			t.Errorf("%s: auth %q", c.kind, (*got)[0].header.Get("Authorization"))
		}
		if c.kind == "elevenlabs" && (*got)[0].header.Get("xi-api-key") != testKey {
			t.Errorf("elevenlabs: no xi-api-key")
		}
	}
	// Deepgram names read well.
	e, _ := New("deepgram", srv.URL, testKey)
	vs, _ := e.Voices(context.Background())
	if vs[0].Name != "Zeus (American)" || vs[0].Lang != "en-US" {
		t.Errorf("deepgram voice %+v", vs[0])
	}
}

func TestErrorsAreRedacted(t *testing.T) {
	srv, _ := fake(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"Incorrect API key provided: ` + r.Header.Get("Authorization") + `"}}`))
	})
	e, _ := New("openai", srv.URL, testKey)
	_, err := e.Synthesize(context.Background(), Request{Text: "hi"})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 401 {
		t.Fatalf("err %v", err)
	}
	if strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), testKey[:12]) {
		t.Fatalf("key leaked: %v", err)
	}
}

func TestChecks(t *testing.T) {
	if _, err := New("browser", "", ""); !errors.Is(err, ErrBrowserVoice) {
		t.Fatalf("browser: %v", err)
	}
	if _, err := New("nope", "", "k"); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if _, err := New("openai", "", ""); err == nil {
		t.Fatal("openai without key accepted")
	}
	if _, err := New("openai-compatible", "", ""); err == nil {
		t.Fatal("compatible without base URL accepted")
	}
	if _, err := New("openai-compatible", "file:///etc/passwd", ""); err == nil {
		t.Fatal("file URL accepted")
	}
	srv, got := fake(t, mp3Reply)
	e, _ := New("deepgram", srv.URL, testKey)
	if _, err := e.Synthesize(context.Background(), Request{Text: strings.Repeat("a", 2001)}); !errors.Is(err, ErrText) {
		t.Fatalf("long text: %v", err)
	}
	if _, err := e.Synthesize(context.Background(), Request{Text: "  "}); !errors.Is(err, ErrText) {
		t.Fatalf("empty text: %v", err)
	}
	if len(*got) != 0 {
		t.Fatal("invalid text was sent")
	}
	// A JSON body with status 200 is not audio.
	srv2, _ := fake(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"detail":"model not loaded"}`)) })
	e2, _ := New("openai-compatible", srv2.URL, "")
	if _, err := e2.Synthesize(context.Background(), Request{Text: "hi"}); err == nil || !strings.Contains(err.Error(), "model not loaded") {
		t.Fatalf("json reply: %v", err)
	}
}
