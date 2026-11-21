package tts

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// ---- Fish Audio: POST {base}/v1/tts, voices from GET {base}/model ----

type fish struct{ *httpc }

func (e *fish) Synthesize(ctx context.Context, r Request) (*Audio, error) {
	r, err := e.check(r)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"text": r.Text, "format": "mp3", "mp3_bitrate": 128, "latency": "normal"}
	if r.Voice != "" {
		body["reference_id"] = r.Voice
	}
	if r.Speed > 0 {
		body["prosody"] = map[string]any{"speed": clamp(r.Speed, 0.5, 2)}
	}
	b, _, err := e.send(ctx, "POST", e.url("/v1/tts", nil), body, func(h http.Header) {
		bearer(e.key)(h)
		if r.Model != "" {
			h.Set("model", r.Model)
		}
	})
	if err != nil {
		return nil, err
	}
	return e.audio(b)
}

type fishModels struct {
	Items []struct {
		ID        string   `json:"_id"`
		Title     string   `json:"title"`
		Type      string   `json:"type"`
		Languages []string `json:"languages"`
	} `json:"items"`
}

// Voices lists the workspace's own voice models first, then the most
// popular public ones.
func (e *fish) Voices(ctx context.Context) ([]Voice, error) {
	var out []Voice
	seen := map[string]bool{}
	for _, q := range []url.Values{
		{"self": {"true"}, "page_size": {"100"}, "sort_by": {"created_at"}},
		{"page_size": {"50"}, "sort_by": {"score"}},
	} {
		var m fishModels
		if err := e.getJSON(ctx, e.url("/model", q), bearer(e.key), &m); err != nil {
			return nil, err
		}
		for _, it := range m.Items {
			if it.ID == "" || seen[it.ID] || (it.Type != "" && it.Type != "tts") {
				continue
			}
			seen[it.ID] = true
			out = append(out, Voice{ID: it.ID, Name: or(it.Title, it.ID), Lang: strings.Join(it.Languages, ", ")})
		}
	}
	return out, nil
}

// ---- ElevenLabs: POST {base}/v1/text-to-speech/{voice}, voices from GET {base}/v2/voices ----

type elevenLabs struct{ *httpc }

func (e *elevenLabs) auth(h http.Header) { h.Set("xi-api-key", e.key) }

func (e *elevenLabs) Synthesize(ctx context.Context, r Request) (*Audio, error) {
	r, err := e.check(r)
	if err != nil {
		return nil, err
	}
	if r.Voice == "" {
		return nil, &APIError{Kind: e.kind, Status: 400, Message: "pick a voice"}
	}
	body := map[string]any{"text": r.Text, "model_id": r.Model}
	if r.Speed > 0 {
		body["voice_settings"] = map[string]any{"speed": clamp(r.Speed, 0.7, 1.2)}
	}
	u := e.url("/v1/text-to-speech/"+url.PathEscape(r.Voice), url.Values{"output_format": {"mp3_44100_128"}})
	b, _, err := e.send(ctx, "POST", u, body, e.auth)
	if err != nil {
		return nil, err
	}
	return e.audio(b)
}

func (e *elevenLabs) Voices(ctx context.Context) ([]Voice, error) {
	var out []Voice
	token := ""
	for page := 0; page < 5; page++ {
		q := url.Values{"page_size": {"100"}, "include_total_count": {"false"}}
		if token != "" {
			q.Set("next_page_token", token)
		}
		var v struct {
			Voices []struct {
				VoiceID string            `json:"voice_id"`
				Name    string            `json:"name"`
				Labels  map[string]string `json:"labels"`
			} `json:"voices"`
			HasMore bool   `json:"has_more"`
			Next    string `json:"next_page_token"`
		}
		if err := e.getJSON(ctx, e.url("/v2/voices", q), e.auth, &v); err != nil {
			return nil, err
		}
		for _, x := range v.Voices {
			lang := x.Labels["language"]
			if a := x.Labels["accent"]; a != "" && lang == "" {
				lang = a
			}
			out = append(out, Voice{ID: x.VoiceID, Name: or(x.Name, x.VoiceID), Lang: lang})
		}
		if !v.HasMore || v.Next == "" {
			break
		}
		token = v.Next
	}
	return out, nil
}

// ---- OpenAI and OpenAI-compatible: POST {base}/audio/speech ----

type openAI struct{ *httpc }

// openAIVoices are OpenAI's built-in voices (the API has no voice list).
var openAIVoices = []string{"alloy", "ash", "ballad", "cedar", "coral", "echo", "fable", "marin", "nova", "onyx", "sage", "shimmer", "verse"}

func (e *openAI) Synthesize(ctx context.Context, r Request) (*Audio, error) {
	r, err := e.check(r)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"model": r.Model, "input": r.Text, "voice": r.Voice, "response_format": "mp3"}
	if r.Speed > 0 {
		body["speed"] = clamp(r.Speed, 0.25, 4)
	}
	b, _, err := e.send(ctx, "POST", e.url("/audio/speech", nil), body, bearer(e.key))
	if err != nil {
		return nil, err
	}
	return e.audio(b)
}

func staticVoices(ids []string) []Voice {
	out := make([]Voice, len(ids))
	for i, id := range ids {
		out[i] = Voice{ID: id, Name: strings.ToUpper(id[:1]) + id[1:]}
	}
	return out
}

// Voices is the static list for OpenAI. Self-hosted servers are asked
// GET {base}/audio/voices (Kokoro-FastAPI); servers without it (openedai-
// speech, LocalAI) get the OpenAI names, which they map to their own voices.
func (e *openAI) Voices(ctx context.Context) ([]Voice, error) {
	if e.kind == KindOpenAI {
		return staticVoices(openAIVoices), nil
	}
	var v struct {
		Voices []any `json:"voices"`
	}
	if err := e.getJSON(ctx, e.url("/audio/voices", nil), bearer(e.key), &v); err != nil {
		if ae, ok := err.(*APIError); ok && (ae.Status == 404 || ae.Status == 405) {
			return staticVoices(openAIVoices), nil
		}
		return nil, err
	}
	var out []Voice
	for _, x := range v.Voices {
		switch t := x.(type) {
		case string:
			out = append(out, Voice{ID: t, Name: t})
		case map[string]any:
			id, _ := t["id"].(string)
			if id == "" {
				id, _ = t["name"].(string)
			}
			name, _ := t["name"].(string)
			lang, _ := t["language"].(string)
			if id != "" {
				out = append(out, Voice{ID: id, Name: or(name, id), Lang: lang})
			}
		}
	}
	if len(out) == 0 {
		return staticVoices(openAIVoices), nil
	}
	return out, nil
}

// ---- Cartesia: POST {base}/tts/bytes, voices from GET {base}/voices ----

// CartesiaVersion is the Cartesia-Version header value.
const CartesiaVersion = "2026-08-14"

type cartesia struct{ *httpc }

func (e *cartesia) auth(h http.Header) {
	bearer(e.key)(h)
	h.Set("Cartesia-Version", CartesiaVersion)
}

func (e *cartesia) Synthesize(ctx context.Context, r Request) (*Audio, error) {
	r, err := e.check(r)
	if err != nil {
		return nil, err
	}
	if r.Voice == "" {
		return nil, &APIError{Kind: e.kind, Status: 400, Message: "pick a voice"}
	}
	body := map[string]any{
		"model_id":      r.Model,
		"transcript":    r.Text,
		"voice":         map[string]any{"id": r.Voice},
		"output_format": map[string]any{"container": "mp3", "sample_rate": 44100, "bit_rate": 128000},
	}
	if r.Speed > 0 {
		body["generation_config"] = map[string]any{"speed": clamp(r.Speed, 0.6, 1.5)}
	}
	b, _, err := e.send(ctx, "POST", e.url("/tts/bytes", nil), body, e.auth)
	if err != nil {
		return nil, err
	}
	return e.audio(b)
}

func (e *cartesia) Voices(ctx context.Context) ([]Voice, error) {
	var out []Voice
	after := ""
	for page := 0; page < 5; page++ {
		q := url.Values{"limit": {"100"}}
		if after != "" {
			q.Set("starting_after", after)
		}
		var v struct {
			Data []struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Language string `json:"language"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			Next    string `json:"next_page"`
		}
		if err := e.getJSON(ctx, e.url("/voices", q), e.auth, &v); err != nil {
			return nil, err
		}
		for _, x := range v.Data {
			out = append(out, Voice{ID: x.ID, Name: or(x.Name, x.ID), Lang: x.Language})
		}
		if !v.HasMore || len(v.Data) == 0 {
			break
		}
		after = or(v.Next, v.Data[len(v.Data)-1].ID)
	}
	return out, nil
}

// ---- Deepgram Aura: POST {base}/v1/speak?model=..., voices from GET {base}/v1/models ----

// For Deepgram the voice is the model name (aura-2-thalia-en).
type deepgram struct{ *httpc }

func (e *deepgram) auth(h http.Header) { h.Set("Authorization", "Token "+e.key) }

func (e *deepgram) Synthesize(ctx context.Context, r Request) (*Audio, error) {
	r, err := e.check(r)
	if err != nil {
		return nil, err
	}
	model := or(r.Voice, r.Model)
	q := url.Values{"model": {model}, "encoding": {"mp3"}, "bit_rate": {"48000"}}
	if r.Speed > 0 {
		q.Set("speed", strconv.FormatFloat(clamp(r.Speed, 0.7, 1.5), 'f', -1, 64))
	}
	b, _, err := e.send(ctx, "POST", e.url("/v1/speak", q), map[string]string{"text": r.Text}, e.auth)
	if err != nil {
		return nil, err
	}
	return e.audio(b)
}

func (e *deepgram) Voices(ctx context.Context) ([]Voice, error) {
	var v struct {
		TTS []struct {
			Name      string   `json:"name"`
			Canonical string   `json:"canonical_name"`
			Languages []string `json:"languages"`
			Metadata  struct {
				Accent string `json:"accent"`
			} `json:"metadata"`
		} `json:"tts"`
	}
	if err := e.getJSON(ctx, e.url("/v1/models", nil), e.auth, &v); err != nil {
		return nil, err
	}
	out := []Voice{}
	for _, x := range v.TTS {
		if x.Canonical == "" {
			continue
		}
		name := x.Name
		if name != "" {
			name = strings.ToUpper(name[:1]) + name[1:]
		}
		if x.Metadata.Accent != "" {
			name += " (" + x.Metadata.Accent + ")"
		}
		lang := ""
		if len(x.Languages) > 0 {
			lang = x.Languages[len(x.Languages)-1]
		}
		out = append(out, Voice{ID: x.Canonical, Name: or(strings.TrimSpace(name), x.Canonical), Lang: lang})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
