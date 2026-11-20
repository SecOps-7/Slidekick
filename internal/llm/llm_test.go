package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var allKinds = []Kind{KindOpenAI, KindCompatible, KindAnthropic, KindGoogle}

func baseFor(k Kind, srv string) string {
	switch k {
	case KindAnthropic:
		return srv
	case KindGoogle:
		return srv + "/v1beta"
	default:
		return srv + "/v1"
	}
}

// firstTextEvent writes one text delta in kind's wire format.
func firstTextEvent(k Kind, s *sse) {
	switch k {
	case KindAnthropic:
		s.event("message_start", `{"type":"message_start","message":{"id":"msg_c","type":"message","role":"assistant","content":[]}}`)
		s.event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		s.event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Working"}}`)
	case KindGoogle:
		s.event("", `{"candidates":[{"content":{"parts":[{"text":"Working"}],"role":"model"},"index":0}]}`)
	default:
		s.event("", `{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Working"},"finish_reason":null}]}`)
	}
}

func TestStreamCancelMidStream(t *testing.T) {
	for _, k := range allKinds {
		t.Run(string(k), func(t *testing.T) {
			released := make(chan struct{})
			f := newFake(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
				s := startSSE(w)
				firstTextEvent(k, s)
				// Then stall like a slow model, until the client goes away.
				select {
				case <-r.Context().Done():
				case <-time.After(20 * time.Second):
				}
				close(released)
			})
			p := Provider{Kind: k, BaseURL: baseFor(k, f.URL), APIKey: "key-cancel-test-123456"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			got := make(chan struct{}, 1)
			onText := func(string) {
				select {
				case got <- struct{}{}:
				default:
				}
			}
			go func() {
				<-got
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()
			start := time.Now()
			msg, err := Stream(ctx, nil, p, Request{Model: "m", Messages: []Message{{Role: "user", Text: "hi"}}}, onText)
			elapsed := time.Since(start)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("want context.Canceled, got %v", err)
			}
			if elapsed > 3*time.Second {
				t.Fatalf("Stream took %v after cancel", elapsed)
			}
			if msg.Text != "Working" {
				t.Fatalf("partial text %q", msg.Text)
			}
			select {
			case <-released:
			case <-time.After(3 * time.Second):
				t.Fatal("server never saw the client disconnect")
			}
		})
	}
}

// The fake echoes the key the way providers do, in full and abbreviated.
func TestKeyNeverInErrors(t *testing.T) {
	const key = "sk-live-AbCdEfGh0123456789IjKlMnOpQrStUv"
	echo := func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":{"message":"Incorrect API key provided: %s. Also seen as %s...%s and %s","type":"invalid_request_error","code":"invalid_api_key"}}`,
			key, key[:12], key[len(key)-10:], url.QueryEscape(key))
	}
	streamErr := func(k Kind) func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		return func(w http.ResponseWriter, _ *http.Request, _ []byte) {
			s := startSSE(w)
			msg := `{"error":{"type":"overloaded_error","message":"key ` + key + ` overloaded"}}`
			if k == KindAnthropic {
				s.event("error", `{"type":"error","error":{"type":"overloaded_error","message":"key `+key+` overloaded"}}`)
				return
			}
			s.event("", msg)
		}
	}
	for _, k := range allKinds {
		t.Run(string(k), func(t *testing.T) {
			f := newFake(t, echo, echo, streamErr(k))
			p := Provider{Kind: k, BaseURL: baseFor(k, f.URL), APIKey: key}
			req := Request{Model: "m", Messages: []Message{{Role: "user", Text: "hi"}}}

			_, err1 := Stream(context.Background(), nil, p, req, nil)
			_, err2 := ListModels(context.Background(), nil, p)
			_, err3 := Stream(context.Background(), nil, p, req, nil)
			for i, err := range []error{err1, err2, err3} {
				if err == nil {
					t.Fatalf("call %d: want error", i)
				}
				assertNoKey(t, err.Error(), key)
				if len(err.Error()) > 400 {
					t.Fatalf("call %d: error too long (%d)", i, len(err.Error()))
				}
			}
			var ae *APIError
			if !errors.As(err1, &ae) || ae.Status != http.StatusUnauthorized || !strings.Contains(err1.Error(), "401") {
				t.Fatalf("want APIError 401, got %#v", err1)
			}
			if !strings.Contains(err1.Error(), "Incorrect API key provided") {
				t.Fatalf("provider message lost: %v", err1)
			}
			if !strings.Contains(err3.Error(), "overloaded") {
				t.Fatalf("stream error message lost: %v", err3)
			}
		})
	}
}

func assertNoKey(t *testing.T, s, key string) {
	t.Helper()
	for i := 0; i+8 <= len(key); i++ {
		if strings.Contains(s, key[i:i+8]) {
			t.Fatalf("error leaks key fragment %q: %s", key[i:i+8], s)
		}
	}
	if strings.Contains(s, url.QueryEscape(key)) {
		t.Fatalf("error leaks escaped key: %s", s)
	}
}

func TestRedact(t *testing.T) {
	key := "sk-proj-ABCDEFGHIJKLMNOP"
	cases := []struct{ in, want string }{
		{"no secret here", "no secret here"},
		{"key=" + key + "!", "key=[REDACTED]!"},
		{"provided: sk-proj-ABCD...MNOP", "provided: [REDACTED]...MNOP"}, // 12-char prefix run
		{"tail EFGHIJKLMNOP end", "tail [REDACTED] end"},
		{"short sk-pro is fine", "short sk-pro is fine"}, // 6 chars < 8
	}
	for _, c := range cases {
		if got := Redact(c.in, key); got != c.want {
			t.Errorf("Redact(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := Redact("anything", ""); got != "anything" {
		t.Errorf("empty key changed text: %q", got)
	}
}

func TestListModels(t *testing.T) {
	t.Run("openai", func(t *testing.T) {
		f := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
			fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-5","object":"model","created":1,"owned_by":"openai"},{"id":"gpt-4.1","object":"model"}]}`)
		})
		p := Provider{Kind: KindOpenAI, BaseURL: f.URL + "/v1", APIKey: "sk-x"}
		ids, err := ListModels(context.Background(), nil, p)
		if err != nil || !reflect.DeepEqual(ids, []string{"gpt-4.1", "gpt-5"}) {
			t.Fatalf("ids %v err %v", ids, err)
		}
		rq := f.requests()[0]
		if rq.Method != http.MethodGet || rq.Path != "/v1/models" || rq.Header.Get("Authorization") != "Bearer sk-x" {
			t.Fatalf("request %+v", rq)
		}
	})
	t.Run("anthropic", func(t *testing.T) {
		f := newFake(t,
			func(w http.ResponseWriter, _ *http.Request, _ []byte) {
				fmt.Fprint(w, `{"data":[{"type":"model","id":"claude-opus-5-5","display_name":"Claude Opus 5.5"}],"has_more":true,"first_id":"claude-opus-5-5","last_id":"claude-opus-5-5"}`)
			},
			func(w http.ResponseWriter, _ *http.Request, _ []byte) {
				fmt.Fprint(w, `{"data":[{"type":"model","id":"claude-haiku-5-5"}],"has_more":false,"first_id":"claude-haiku-5-5","last_id":"claude-haiku-5-5"}`)
			})
		p := Provider{Kind: KindAnthropic, BaseURL: f.URL, APIKey: "sk-ant-x"}
		ids, err := ListModels(context.Background(), nil, p)
		if err != nil || !reflect.DeepEqual(ids, []string{"claude-haiku-5-5", "claude-opus-5-5"}) {
			t.Fatalf("ids %v err %v", ids, err)
		}
		reqs := f.requests()
		if reqs[0].Path != "/v1/models" || reqs[0].Header.Get("x-api-key") != "sk-ant-x" || reqs[0].Header.Get("anthropic-version") == "" {
			t.Fatalf("request %+v", reqs[0])
		}
		if q, _ := url.ParseQuery(reqs[1].Query); q.Get("after_id") != "claude-opus-5-5" {
			t.Fatalf("second page query %q", reqs[1].Query)
		}
	})
	t.Run("google", func(t *testing.T) {
		f := newFake(t,
			func(w http.ResponseWriter, _ *http.Request, _ []byte) {
				fmt.Fprint(w, `{"models":[{"name":"models/gemini-3-pro","supportedGenerationMethods":["generateContent","countTokens"]},{"name":"models/text-embedding-004","supportedGenerationMethods":["embedContent"]}],"nextPageToken":"p2"}`)
			},
			func(w http.ResponseWriter, _ *http.Request, _ []byte) {
				fmt.Fprint(w, `{"models":[{"name":"models/gemini-2.5-flash","supportedGenerationMethods":["generateContent"]}]}`)
			})
		p := Provider{Kind: KindGoogle, BaseURL: f.URL + "/v1beta", APIKey: "AIza-x"}
		ids, err := ListModels(context.Background(), nil, p)
		if err != nil || !reflect.DeepEqual(ids, []string{"gemini-2.5-flash", "gemini-3-pro"}) {
			t.Fatalf("ids %v err %v", ids, err)
		}
		reqs := f.requests()
		if reqs[0].Path != "/v1beta/models" || reqs[0].Header.Get("x-goog-api-key") != "AIza-x" || strings.Contains(reqs[0].Query, "AIza") {
			t.Fatalf("request %+v", reqs[0])
		}
		if q, _ := url.ParseQuery(reqs[1].Query); q.Get("pageToken") != "p2" {
			t.Fatalf("second page query %q", reqs[1].Query)
		}
	})
}

func countingServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{"data":[]}`)
	}))
	t.Cleanup(s.Close)
	return s, &hits
}

func TestNewClientRefusesOtherHost(t *testing.T) {
	allowed, _ := countingServer(t)
	c, err := NewClient(Provider{Kind: KindOpenAI, BaseURL: allowed.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	// Same listener, different host name: must be refused without connecting.
	port := strings.TrimPrefix(allowed.URL, "http://127.0.0.1:")
	if _, err := c.Get("http://localhost:" + port + "/v1/models"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("request to another host: %v", err)
	}
	if _, err := c.Get("https://127.0.0.1:" + port + "/v1/models"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("request with another scheme: %v", err)
	}
	resp, err := c.Get(allowed.URL + "/v1/models")
	if err != nil {
		t.Fatalf("allowed origin failed: %v", err)
	}
	resp.Body.Close()
}

func TestNewClientRefusesOtherPort(t *testing.T) {
	allowed, _ := countingServer(t)
	other, otherHits := countingServer(t)
	c, err := NewClient(Provider{Kind: KindOpenAI, BaseURL: allowed.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(other.URL + "/v1/models"); err == nil {
		t.Fatal("request to another port succeeded")
	}
	// The dialer itself also refuses, even if something bypassed the guard.
	tr := c.Transport.(*originGuard).next.(*http.Transport)
	if conn, err := tr.DialContext(context.Background(), "tcp", strings.TrimPrefix(other.URL, "http://")); err == nil {
		conn.Close()
		t.Fatal("dialer connected to another port")
	}
	if conn, err := tr.DialContext(context.Background(), "tcp", "localhost:"+strings.TrimPrefix(allowed.URL, "http://127.0.0.1:")); err == nil {
		conn.Close()
		t.Fatal("dialer connected to another host name")
	}
	if otherHits.Load() != 0 {
		t.Fatalf("other server got %d hits", otherHits.Load())
	}
}

func TestNewClientRefusesRedirects(t *testing.T) {
	var targetHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	})
	mux.HandleFunc("/elsewhere", func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		fmt.Fprint(w, `{"data":[{"id":"x"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := Provider{Kind: KindOpenAI, BaseURL: srv.URL + "/v1"}
	if _, err := ListModels(context.Background(), nil, p); err == nil {
		t.Fatal("redirect was followed")
	}
	if targetHits.Load() != 0 {
		t.Fatal("redirect target was requested")
	}
}

func TestNewClientTimeouts(t *testing.T) {
	c, err := NewClient(Provider{Kind: KindOpenAI, BaseURL: "https://api.openai.com/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Timeout != 0 {
		t.Fatalf("overall timeout %v would cut long streams", c.Timeout)
	}
	tr := c.Transport.(*originGuard).next.(*http.Transport)
	if tr.ResponseHeaderTimeout != 60*time.Second || tr.Proxy != nil {
		t.Fatalf("transport %+v", tr)
	}
}

func TestValidateBaseURL(t *testing.T) {
	good := []string{
		"https://api.openai.com/v1",
		"https://api.anthropic.com",
		"https://generativelanguage.googleapis.com/v1beta",
		"http://172.16.5.9:11434/v1",
		"http://[fd00::1]:8080/v1",
		"HTTPS://Example.com/",
		"http://localhost:1234",
	}
	for _, s := range good {
		if _, err := ValidateBaseURL(s); err != nil {
			t.Errorf("ValidateBaseURL(%q) = %v, want ok", s, err)
		}
	}
	bad := []string{
		"",
		"api.openai.com/v1",
		"ftp://example.com",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"https://user:pass@example.com",
		"https://user@example.com",
		"https://example.com/v1?key=abc",
		"https://example.com/v1?",
		"https://example.com/v1#frag",
		"https://:443/v1",
		"http://example.com:99999",
		"http://example.com:/v1",
		"http:///v1",
	}
	for _, s := range bad {
		if _, err := ValidateBaseURL(s); err == nil {
			t.Errorf("ValidateBaseURL(%q) accepted", s)
		}
	}
}

func TestUnknownKind(t *testing.T) {
	p := Provider{Kind: "nope", BaseURL: "http://127.0.0.1:1"}
	if _, err := Stream(context.Background(), nil, p, Request{Model: "m"}, nil); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if _, err := ListModels(context.Background(), nil, p); err == nil {
		t.Fatal("unknown kind accepted")
	}
}
