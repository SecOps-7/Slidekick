package server_test

import (
	"net/url"
	"strings"
	"testing"
)

// A provider keeps several named voices with one default; presenters (also
// on a presenter link) list them to pick one, without the URL or the key.
func TestTTSNamedVoices(t *testing.T) {
	e := newTTSServer(t)
	admin := e.withAdmin(t)
	voices := []map[string]any{{"id": "v-1", "name": "  Aria   (warm) "}, {"id": "v-2", "name": ""}, {"id": "v-1", "name": "duplicate"}}
	code, out := admin.do("PUT", "/api/admin/tts/eleven", map[string]any{"name": "ElevenLabs", "kind": "elevenlabs", "apiKey": ttsKey, "defaultVoice": "v-2", "voices": voices})
	if code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	got := toJSON(out["voices"])
	if got != `[{"id":"v-1","name":"Aria (warm)"},{"id":"v-2","name":"v-2"}]` || out["defaultVoice"] != "v-2" {
		t.Fatalf("voices cleaned and named: %s default %v", got, out["defaultVoice"])
	}

	// The default must be one of the voices, and voices have limits.
	for _, bad := range []map[string]any{
		{"defaultVoice": "v-9", "voices": []map[string]any{{"id": "v-1"}}},
		{"defaultVoice": "", "voices": []map[string]any{{"id": "v-1", "name": strings.Repeat("x", 81)}}},
		{"defaultVoice": "", "voices": []map[string]any{{"id": strings.Repeat("x", 201)}}},
	} {
		in := map[string]any{"name": "ElevenLabs", "kind": "elevenlabs"}
		for k, v := range bad {
			in[k] = v
		}
		if code, out := admin.do("PUT", "/api/admin/tts/eleven", in); code != 422 {
			t.Fatalf("%v accepted: %d %v", bad, code, out)
		}
	}
	// A save without voices keeps them; a caller that only sets a new default voice gets it added.
	if _, out = admin.do("PUT", "/api/admin/tts/eleven", map[string]any{"name": "ElevenLabs", "kind": "elevenlabs", "defaultVoice": "v-3"}); toJSON(out["voices"]) != `[{"id":"v-1","name":"Aria (warm)"},{"id":"v-2","name":"v-2"},{"id":"v-3","name":"v-3"}]` {
		t.Fatalf("save without voices: %s", toJSON(out["voices"]))
	}
	// Removing every voice falls back to the provider's own default voice.
	if code, out = admin.do("PUT", "/api/admin/tts/eleven", map[string]any{"name": "ElevenLabs", "kind": "elevenlabs", "defaultVoice": "", "voices": []any{}}); code != 200 || toJSON(out["voices"]) != `[]` {
		t.Fatalf("no voices: %d %v", code, out)
	}
	admin.do("PUT", "/api/admin/tts/eleven", map[string]any{"name": "ElevenLabs", "kind": "elevenlabs", "defaultVoice": "v-1", "voices": []map[string]any{{"id": "v-1", "name": "Aria"}, {"id": "v-2", "name": "Ben"}}})

	// The presenter view lists the voices for its deck, on a presenter link too.
	_, d := admin.do("POST", "/api/decks", map[string]any{"title": "Talk"})
	id := d["deckId"].(string)
	_, l := admin.do("POST", "/api/decks/"+id+"/links", map[string]any{"kind": "present"})
	u, _ := url.Parse(l["url"].(string))
	code, list := anon(t, "GET", e.ts.URL+"/api/decks/"+id+"/voices?p="+url.QueryEscape(u.Query().Get("p")), nil)
	s := toJSON(list)
	if code != 200 || !strings.Contains(s, `"voices":[{"id":"v-1","name":"Aria"},{"id":"v-2","name":"Ben"}]`) || !strings.Contains(s, `"id":"browser"`) {
		t.Fatalf("presenter link voice list: %d %s", code, s)
	}
	if strings.Contains(s, "baseUrl") || strings.Contains(s, "hasKey") || strings.Contains(s, ttsKey) {
		t.Fatalf("voice list leaks provider details: %s", s)
	}
	if code, _ := anon(t, "GET", e.ts.URL+"/api/decks/"+id+"/voices", nil); code != 401 && code != 404 {
		t.Fatalf("voice list without a link: %d", code)
	}
}
