// Package narration connects text-to-speech providers (internal/tts) to
// Slidekick: admins configure providers and the default narration voice,
// and the presenter calls Speak to turn speaker notes into audio, which is
// cached in memory and served from a short-lived, unguessable URL.
package narration

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/SecOps-7/slidekick/internal/llm"
	"github.com/SecOps-7/slidekick/internal/secret"
	"github.com/SecOps-7/slidekick/internal/store"
	"github.com/SecOps-7/slidekick/internal/tts"
)

// BrowserID is the built-in provider that the browser speaks itself.
const BrowserID = "browser"

// MaxText is the longest text Speak takes; longer engine limits are met by
// splitting it at sentence boundaries and joining the MP3 parts.
const MaxText = 20000

// SampleText is what "Play sample" says.
const SampleText = "Hello! This is how this voice sounds when Slidekick reads your speaker notes."

var ErrValidation = errors.New("invalid")

type Service struct {
	Store *store.Store
	Box   *secret.Box
	// Engine builds the engine for a provider; nil uses tts.New.
	Engine func(kind, baseURL, apiKey string) (tts.Engine, error)

	once  sync.Once
	cache *Cache
}

// CacheTTL is how long unused audio (and its URL) stays available.
const CacheTTL = 30 * time.Minute

func (s *Service) audioCache() *Cache {
	s.once.Do(func() { s.cache = NewCache(64<<20, CacheTTL) })
	return s.cache
}

// ---- provider administration (admins only; enforced by the HTTP layer) ----

type ProviderInput struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	BaseURL      string  `json:"baseUrl"`
	APIKey       *string `json:"apiKey,omitempty"` // nil keeps the stored key; "" removes it
	DefaultVoice string  `json:"defaultVoice"`
	DefaultModel string  `json:"defaultModel"`
	// Voices replaces the provider's named voices; nil keeps them.
	Voices *[]store.TTSVoice `json:"voices,omitempty"`
}

// MaxVoices is how many voices one provider can keep.
const MaxVoices = 100

// cleanVoices trims, names (a voice without a name is called by its id) and
// de-duplicates a voice list.
func cleanVoices(in []store.TTSVoice) ([]store.TTSVoice, error) {
	out := []store.TTSVoice{}
	seen := map[string]bool{}
	for _, v := range in {
		v.ID, v.Name = strings.TrimSpace(v.ID), strings.Join(strings.Fields(v.Name), " ")
		if v.ID == "" || seen[v.ID] {
			continue
		}
		if len(v.ID) > 200 {
			return nil, fmt.Errorf("%w: voice id %.40q... is too long", ErrValidation, v.ID)
		}
		if v.Name == "" {
			v.Name = v.ID
		}
		if utf8.RuneCountInString(v.Name) > 80 {
			return nil, fmt.Errorf("%w: voice name %q is longer than 80 characters", ErrValidation, v.Name)
		}
		seen[v.ID] = true
		out = append(out, v)
	}
	if len(out) > MaxVoices {
		return nil, fmt.Errorf("%w: a provider keeps at most %d voices", ErrValidation, MaxVoices)
	}
	return out, nil
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func keyPurpose(id string) string { return "tts:" + id }

func (s *Service) SaveProvider(ctx context.Context, in ProviderInput) (*store.TTSProvider, error) {
	in.ID = strings.ToLower(strings.TrimSpace(in.ID))
	if !slugRe.MatchString(in.ID) || in.ID == BrowserID {
		return nil, fmt.Errorf("%w: id must be a short slug (a-z, 0-9, -) other than %q", ErrValidation, BrowserID)
	}
	kind := tts.Kind(in.Kind)
	inf, ok := tts.Defaults(kind)
	if !ok {
		return nil, fmt.Errorf("%w: kind must be one of fish, elevenlabs, openai, cartesia, deepgram or openai-compatible", ErrValidation)
	}
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if in.BaseURL == "" {
		in.BaseURL = inf.BaseURL
	}
	if in.BaseURL == "" {
		return nil, fmt.Errorf("%w: a self-hosted server needs its base URL (e.g. http://172.16.5.9:8880/v1)", ErrValidation)
	}
	if _, err := llm.ValidateBaseURL(in.BaseURL); err != nil {
		return nil, fmt.Errorf("%w: base URL: %v", ErrValidation, strings.TrimPrefix(err.Error(), "llm: "))
	}
	if strings.TrimSpace(in.Name) == "" {
		in.Name = in.ID
	}
	clean := func(v string) string {
		v = strings.TrimSpace(v)
		if len(v) > 200 {
			v = v[:200]
		}
		return v
	}
	p, err := s.Store.GetTTSProvider(ctx, in.ID)
	if errors.Is(err, store.ErrNotFound) {
		p, err = &store.TTSProvider{ID: in.ID}, nil
	}
	if err != nil {
		return nil, err
	}
	if in.APIKey != nil {
		if *in.APIKey == "" {
			p.KeyEnc = ""
		} else if p.KeyEnc, err = s.Box.Seal(strings.TrimSpace(*in.APIKey), keyPurpose(in.ID)); err != nil {
			return nil, err
		}
	}
	p.Name, p.Kind, p.BaseURL = strings.TrimSpace(in.Name), string(kind), in.BaseURL
	p.DefaultVoice, p.DefaultModel = clean(in.DefaultVoice), clean(in.DefaultModel)
	if in.Voices != nil {
		if p.Voices, err = cleanVoices(*in.Voices); err != nil {
			return nil, err
		}
	}
	if p.DefaultVoice != "" && !hasVoice(p.Voices, p.DefaultVoice) {
		if in.Voices != nil {
			return nil, fmt.Errorf("%w: the default voice must be one of the provider's voices", ErrValidation)
		}
		p.Voices = append(p.Voices, store.TTSVoice{ID: p.DefaultVoice, Name: p.DefaultVoice}) // a caller that only sets the default
	}
	if p.Voices == nil {
		p.Voices = []store.TTSVoice{}
	}
	if err := s.Store.SaveTTSProvider(ctx, p); err != nil {
		return nil, err
	}
	p.HasKey = p.KeyEnc != ""
	return p, nil
}

func hasVoice(vs []store.TTSVoice, id string) bool {
	for _, v := range vs {
		if v.ID == id {
			return true
		}
	}
	return false
}

func (s *Service) DeleteProvider(ctx context.Context, id string) error {
	if id == BrowserID {
		return fmt.Errorf("%w: the browser voices are built in", ErrValidation)
	}
	return s.Store.DeleteTTSProvider(ctx, id)
}

// Provider is one entry of the provider list. The key itself is never in it.
type Provider struct {
	store.TTSProvider
	BuiltIn   bool `json:"builtIn"`
	IsDefault bool `json:"isDefault"`
}

// Default is the default narration provider and voice.
type Default struct {
	Provider string `json:"provider"` // BrowserID when none is set
	Voice    string `json:"voice"`
}

func (s *Service) Default(ctx context.Context) Default {
	d := Default{Provider: s.Store.Setting(ctx, store.SettingTTSProvider), Voice: s.Store.Setting(ctx, store.SettingTTSVoice)}
	if d.Provider == "" {
		d.Provider = BrowserID
	}
	return d
}

// SetDefault makes providerID (BrowserID for the browser) and voice the
// narration default. An empty voice uses the provider's default voice.
func (s *Service) SetDefault(ctx context.Context, providerID, voice string) error {
	voice = strings.TrimSpace(voice)
	if len(voice) > 200 {
		return fmt.Errorf("%w: voice id is too long", ErrValidation)
	}
	if providerID == BrowserID || providerID == "" {
		providerID = ""
	} else if _, err := s.Store.GetTTSProvider(ctx, providerID); err != nil {
		return err
	}
	if err := s.Store.SetSetting(ctx, store.SettingTTSProvider, providerID); err != nil {
		return err
	}
	return s.Store.SetSetting(ctx, store.SettingTTSVoice, voice)
}

// Providers lists the browser's built-in voices first, then every
// configured provider, marking the narration default.
func (s *Service) Providers(ctx context.Context) ([]Provider, Default, error) {
	ps, err := s.Store.TTSProviders(ctx)
	if err != nil {
		return nil, Default{}, err
	}
	d := s.Default(ctx)
	out := []Provider{{TTSProvider: store.TTSProvider{ID: BrowserID, Name: "Browser voices (free)", Kind: string(tts.KindBrowser), Voices: []store.TTSVoice{}}, BuiltIn: true, IsDefault: d.Provider == BrowserID}}
	for _, p := range ps {
		out = append(out, Provider{TTSProvider: *p, IsDefault: d.Provider == p.ID})
	}
	return out, d, nil
}

// engine opens a provider's engine; BrowserID gives tts.ErrBrowserVoice.
func (s *Service) engine(ctx context.Context, id string) (*store.TTSProvider, tts.Engine, error) {
	if id == BrowserID {
		return nil, nil, tts.ErrBrowserVoice
	}
	p, err := s.Store.GetTTSProvider(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	key, err := s.Box.Open(p.KeyEnc, keyPurpose(p.ID))
	if err != nil {
		return nil, nil, err
	}
	mk := s.Engine
	if mk == nil {
		mk = tts.New
	}
	e, err := mk(p.Kind, p.BaseURL, key)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrValidation, strings.TrimPrefix(llm.Redact(err.Error(), key), "tts: "))
	}
	return p, e, nil
}

// Voices lists a provider's voices (the key stays server-side). The browser
// provider has none here: the browser lists its own.
func (s *Service) Voices(ctx context.Context, providerID string) ([]tts.Voice, error) {
	if providerID == "" {
		providerID = s.Default(ctx).Provider
	}
	_, e, err := s.engine(ctx, providerID)
	if errors.Is(err, tts.ErrBrowserVoice) {
		return []tts.Voice{}, nil
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	vs, err := e.Voices(ctx)
	if vs == nil {
		vs = []tts.Voice{}
	}
	return vs, err
}

// Sample speaks SampleText with a provider and voice ("" = its default).
func (s *Service) Sample(ctx context.Context, providerID, voice string) (*tts.Audio, error) {
	if providerID == "" {
		return nil, fmt.Errorf("%w: pick a provider", ErrValidation)
	}
	return s.Speak(ctx, providerID, voice, SampleText)
}

// Speak turns text into speech. providerID "" uses the narration default
// provider; voice "" uses the default voice (the instance default when the
// provider is the default one, else the provider's own). The browser
// provider returns tts.ErrBrowserVoice so the caller can fall back to
// window.speechSynthesis. Results are cached and shared: don't modify
// the returned Audio. See AudioURL.
func (s *Service) Speak(ctx context.Context, providerID, voice, text string) (*tts.Audio, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("%w: nothing to say", ErrValidation)
	}
	if utf8.RuneCountInString(text) > MaxText {
		return nil, fmt.Errorf("%w: text is longer than %d characters", ErrValidation, MaxText)
	}
	d := s.Default(ctx)
	if providerID == "" {
		providerID = d.Provider
		if voice == "" {
			voice = d.Voice
		}
	} else if voice == "" && providerID == d.Provider {
		voice = d.Voice
	}
	p, e, err := s.engine(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if voice == "" {
		voice = p.DefaultVoice
	}
	c := s.audioCache()
	key := c.Key(p.ID, voice, p.DefaultModel, text)
	if a, ok := c.Get(key); ok {
		return a, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var out *tts.Audio
	for _, part := range Split(text, tts.Limit(tts.Kind(p.Kind))) {
		a, err := e.Synthesize(ctx, tts.Request{Text: part, Voice: voice, Model: p.DefaultModel})
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = a
			continue
		}
		out.Data = append(out.Data, tts.StripID3(a.Data)...)
		out.Duration += a.Duration
	}
	c.Put(key, out)
	return out, nil
}

// AudioURL returns the path that serves a (a result of Speak, or any audio,
// which is then cached): /api/tts/audio/{hash}.mp3. The URL works until the
// cache drops the audio (30 minutes unused, or memory pressure).
func (s *Service) AudioURL(a *tts.Audio) string {
	return AudioPath(s.audioCache().KeyOf(a))
}

// AudioPath is the URL path for a cache key.
func AudioPath(key string) string { return "/api/tts/audio/" + key + ".mp3" }

// Cached returns cached audio by key (the hash in its URL).
func (s *Service) Cached(key string) (*tts.Audio, bool) { return s.audioCache().Get(key) }

// Split cuts text into parts of at most limit characters, at sentence ends
// where possible, else at spaces, else anywhere.
func Split(text string, limit int) []string {
	var out []string
	for utf8.RuneCountInString(text) > limit {
		r := []rune(text)
		cut := -1
		for i := limit - 1; i > limit/2; i-- { // a sentence end in the second half
			if strings.ContainsRune(".!?;:\n", r[i]) && (i+1 >= len(r) || unicode.IsSpace(r[i+1])) {
				cut = i + 1
				break
			}
		}
		if cut < 0 {
			for i := limit - 1; i > 0; i-- {
				if unicode.IsSpace(r[i]) {
					cut = i
					break
				}
			}
		}
		if cut <= 0 {
			cut = limit
		}
		if p := strings.TrimSpace(string(r[:cut])); p != "" {
			out = append(out, p)
		}
		text = strings.TrimSpace(string(r[cut:]))
	}
	if text != "" {
		out = append(out, text)
	}
	return out
}
