// Package tts is a small, dependency-free client for text-to-speech APIs:
// Fish Audio, ElevenLabs, OpenAI, Cartesia, Deepgram Aura and any
// OpenAI-compatible speech server (Kokoro-FastAPI, openedai-speech, LocalAI).
// Every engine asks for MP3. Clients can reach only their own base URL's
// host (see llm.NewClient), and errors never carry the API key.
package tts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SecOps-7/slidekick/internal/llm"
)

// Engine synthesizes speech with one configured provider.
type Engine interface {
	Synthesize(ctx context.Context, req Request) (*Audio, error)
	Voices(ctx context.Context) ([]Voice, error) // may return a static list
}

// Request is one piece of text to speak. Empty Voice or Model use the
// engine's default; Speed 0 means the provider's normal speed.
type Request struct {
	Text, Voice, Model string
	Speed              float64
}

// Audio is synthesized speech.
type Audio struct {
	Data     []byte
	MIME     string        // audio/mpeg
	Duration time.Duration // best effort, 0 if unknown
}

// Voice is one voice a provider offers.
type Voice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Lang string `json:"lang,omitempty"`
}

// Kind names a provider API.
type Kind string

const (
	KindFish       Kind = "fish"
	KindElevenLabs Kind = "elevenlabs"
	KindOpenAI     Kind = "openai"
	KindCartesia   Kind = "cartesia"
	KindDeepgram   Kind = "deepgram"
	KindCompatible Kind = "openai-compatible"
	// KindBrowser is the free built-in option: the presenter's browser
	// speaks with window.speechSynthesis. The server never synthesizes it.
	KindBrowser Kind = "browser"
)

// Kinds are the server-side engines, in the order the UI lists them.
var Kinds = []Kind{KindFish, KindElevenLabs, KindOpenAI, KindCartesia, KindDeepgram, KindCompatible}

// ErrBrowserVoice is returned for the browser pseudo-provider: the caller
// should fall back to window.speechSynthesis in the browser.
var ErrBrowserVoice = errors.New("tts: browser voices are spoken by the browser, not the server")

// ErrText is wrapped when the text is empty or too long for one request.
var ErrText = errors.New("tts: invalid text")

// MaxChars is the longest text any engine accepts in one request (OpenAI
// allows 4096). Callers split longer text; see Limit.
const MaxChars = 4000

// Limit is the longest text (in characters) kind accepts per request.
func Limit(kind Kind) int {
	if kind == KindDeepgram {
		return 2000 // Aura's documented per-request limit
	}
	return MaxChars
}

// Info describes a kind for setup: its default base URL, model and voice.
type Info struct {
	BaseURL, Model, Voice string
	NeedsKey              bool
}

var info = map[Kind]Info{
	KindFish:       {"https://api.fish.audio", "s2.1-pro", "", true},
	KindElevenLabs: {"https://api.elevenlabs.io", "eleven_multilingual_v2", "", true},
	KindOpenAI:     {"https://api.openai.com/v1", "gpt-4o-mini-tts", "alloy", true},
	KindCartesia:   {"https://api.cartesia.ai", "sonic-3.6", "", true},
	KindDeepgram:   {"https://api.deepgram.com", "", "aura-2-thalia-en", true},
	KindCompatible: {"", "tts-1", "alloy", false},
}

// Defaults returns the defaults for kind; ok is false for unknown kinds.
func Defaults(kind Kind) (Info, bool) {
	i, ok := info[kind]
	return i, ok
}

// Timeout bounds one synthesis or voice-list call end to end.
const Timeout = 90 * time.Second

// maxAudio bounds one response (a 4000-character MP3 is a few MB).
const maxAudio = 32 << 20

// New returns the engine for kind. baseURL "" uses the kind's default (an
// OpenAI-compatible server needs one); apiKey may be "" only for
// openai-compatible.
func New(kind, baseURL, apiKey string) (Engine, error) {
	k := Kind(kind)
	if k == KindBrowser {
		return nil, ErrBrowserVoice
	}
	inf, ok := info[k]
	if !ok {
		return nil, fmt.Errorf("tts: unknown kind %q", kind)
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = inf.BaseURL
	}
	if baseURL == "" {
		return nil, errors.New("tts: an OpenAI-compatible server needs its base URL (e.g. http://172.16.5.9:8880/v1)")
	}
	if inf.NeedsKey && apiKey == "" {
		return nil, fmt.Errorf("tts: %s needs an API key", k)
	}
	c, err := llm.NewClient(llm.Provider{BaseURL: baseURL})
	if err != nil {
		return nil, errors.New(strings.Replace(err.Error(), "llm:", "tts:", 1))
	}
	c.Timeout = Timeout
	h := &httpc{kind: k, base: baseURL, key: apiKey, c: c, info: inf}
	switch k {
	case KindFish:
		return &fish{h}, nil
	case KindElevenLabs:
		return &elevenLabs{h}, nil
	case KindOpenAI, KindCompatible:
		return &openAI{h}, nil
	case KindCartesia:
		return &cartesia{h}, nil
	case KindDeepgram:
		return &deepgram{h}, nil
	}
	return nil, fmt.Errorf("tts: unknown kind %q", kind)
}

// APIError is a non-2xx answer from a provider. Message is redacted.
type APIError struct {
	Kind    Kind
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Kind, e.Status, e.Message)
}

// httpc is what every engine shares.
type httpc struct {
	kind Kind
	base string
	key  string
	c    *http.Client
	info Info
}

func (h *httpc) url(path string, q url.Values) string {
	u := h.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// redact turns any error into one whose text carries no part of the key.
func (h *httpc) redact(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return &APIError{Kind: ae.Kind, Status: ae.Status, Message: trim(llm.Redact(ae.Message, h.key), 300)}
	}
	return errors.New(trim(llm.Redact(err.Error(), h.key), 600))
}

// send performs a request with auth headers set by auth and returns the
// response body (bounded). Non-2xx answers become *APIError.
func (h *httpc) send(ctx context.Context, method, u string, body any, auth func(http.Header)) ([]byte, http.Header, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, nil, h.redact(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	auth(req.Header)
	resp, err := h.c.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, h.redact(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, nil, h.redact(&APIError{Kind: h.kind, Status: resp.StatusCode, Message: errorMessage(b, resp.Status)})
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxAudio+1))
	if err != nil {
		return nil, nil, h.redact(err)
	}
	if len(b) > maxAudio {
		return nil, nil, fmt.Errorf("%s: response is larger than %d MB", h.kind, maxAudio>>20)
	}
	return b, resp.Header, nil
}

func (h *httpc) getJSON(ctx context.Context, u string, auth func(http.Header), v any) error {
	b, _, err := h.send(ctx, "GET", u, nil, auth)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: unexpected voice list: %v", h.kind, err)
	}
	return nil
}

// audio wraps an MP3 answer. A JSON body (some servers answer 200 with an
// error object) is reported as an error.
func (h *httpc) audio(b []byte) (*Audio, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("%s: empty audio", h.kind)
	}
	if b[0] == '{' {
		return nil, h.redact(fmt.Errorf("%s: expected audio, got: %s", h.kind, errorMessage(b, "JSON")))
	}
	return &Audio{Data: b, MIME: "audio/mpeg", Duration: MP3Duration(b)}, nil
}

// check validates text and fills defaults.
func (h *httpc) check(r Request) (Request, error) {
	r.Text = strings.TrimSpace(r.Text)
	if r.Text == "" {
		return r, fmt.Errorf("%w: nothing to say", ErrText)
	}
	if n := utf8.RuneCountInString(r.Text); n > Limit(h.kind) {
		return r, fmt.Errorf("%w: %d characters is more than %s takes at once (%d)", ErrText, n, h.kind, Limit(h.kind))
	}
	if r.Voice == "" {
		r.Voice = h.info.Voice
	}
	if r.Model == "" {
		r.Model = h.info.Model
	}
	if r.Speed < 0 {
		r.Speed = 0
	}
	return r, nil
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func bearer(key string) func(http.Header) {
	return func(h http.Header) {
		if key != "" {
			h.Set("Authorization", "Bearer "+key)
		}
	}
}

// errorMessage pulls a human message out of a provider error body:
// {"error":{"message"}}, {"detail":{"message"}}, {"detail":"..."},
// {"message":"..."}, {"err_msg":"..."}.
func errorMessage(body []byte, fallback string) string {
	var e struct {
		Error  json.RawMessage `json:"error"`
		Detail json.RawMessage `json:"detail"`
		Msg    string          `json:"message"`
		ErrMsg string          `json:"err_msg"`
	}
	if json.Unmarshal(body, &e) == nil {
		for _, raw := range []json.RawMessage{e.Error, e.Detail} {
			var inner struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(raw, &inner) == nil && inner.Message != "" {
				return inner.Message
			}
			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				return s
			}
		}
		if e.Msg != "" {
			return e.Msg
		}
		if e.ErrMsg != "" {
			return e.ErrMsg
		}
	}
	t := strings.Join(strings.Fields(string(body)), " ")
	if t == "" || !utf8.ValidString(t) {
		return fallback
	}
	return t
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "..."
}
