package llm

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// googleToolTurn streams like streamGenerateContent?alt=sse for a Gemini 3
// model: a thought summary part, text over two chunks, then two parallel
// functionCall parts in separate chunks; only the first carries the
// thoughtSignature (Gemini sends function-call args whole, not in fragments).
func googleToolTurn(w http.ResponseWriter, _ *http.Request, _ []byte) {
	s := startSSE(w)
	s.event("", `{"candidates":[{"content":{"parts":[{"text":"Planning two edits","thought":true}],"role":"model"},"index":0}],"modelVersion":"gemini-3-pro"}`)
	s.event("", `{"candidates":[{"content":{"parts":[{"text":"I'll update "}],"role":"model"},"index":0}],"modelVersion":"gemini-3-pro"}`)
	s.event("", `{"candidates":[{"content":{"parts":[{"text":"both slides."}],"role":"model"},"index":0}],"modelVersion":"gemini-3-pro"}`)
	s.event("", `{"candidates":[{"content":{"parts":[{"functionCall":{"id":"fc-a1","name":"set_title","args":{"slide":1,"title":"Q3 \"results\""}},"thoughtSignature":"CiQBVKhc7hXdSIG"}],"role":"model"},"index":0}],"modelVersion":"gemini-3-pro"}`)
	s.event("", `{"candidates":[{"content":{"parts":[{"functionCall":{"id":"fc-b2","name":"add_bullet","args":{"slide":9,"text":"Revenue ↑ 12%"}}}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":80,"candidatesTokenCount":40,"totalTokenCount":120},"modelVersion":"gemini-3-pro"}`)
}

func googleFinalTurn(w http.ResponseWriter, _ *http.Request, _ []byte) {
	s := startSSE(w)
	s.event("", `{"candidates":[{"content":{"parts":[{"text":"Done."}],"role":"model"},"index":0}]}`)
	// Signature arriving on an empty text part in the last chunk.
	s.event("", `{"candidates":[{"content":{"parts":[{"text":"","thoughtSignature":"Zm9v"}],"role":"model"},"finishReason":"STOP","index":0}]}`)
}

func TestGoogleToolRoundTrip(t *testing.T) {
	f := newFake(t, googleToolTurn, googleFinalTurn)
	p := Provider{Kind: KindGoogle, BaseURL: f.URL + "/v1beta", APIKey: "AIzaSyTest0123456789abcdefghij"}
	c, err := NewClient(p)
	if err != nil {
		t.Fatal(err)
	}
	r := Request{Model: "gemini-3-pro", System: "You edit slide decks.", Tools: slideTools, MaxTokens: 2048,
		Messages: []Message{{Role: "user", Text: "Retitle slide 1 and add a bullet to slide 9"}}}
	var sink textSink
	msg, err := Stream(context.Background(), c, p, r, sink.add)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if msg.Text != "I'll update both slides." || sink.joined() != msg.Text || sink.count() != 2 {
		t.Fatalf("text %q / %q (%d)", msg.Text, sink.joined(), sink.count())
	}
	if len(msg.ToolCalls) != 2 || msg.ToolCalls[0].ID != "fc-a1" || msg.ToolCalls[1].ID != "fc-b2" {
		t.Fatalf("tool calls %+v", msg.ToolCalls)
	}
	if msg.ToolCalls[0].Meta[MetaGoogleThoughtSignature] != "CiQBVKhc7hXdSIG" || msg.ToolCalls[1].Meta[MetaGoogleThoughtSignature] != "" {
		t.Fatalf("signatures %+v", msg.ToolCalls)
	}
	jsonEq(t, msg.ToolCalls[0].Args, `{"slide":1,"title":"Q3 \"results\""}`)
	jsonEq(t, msg.ToolCalls[1].Args, `{"slide":9,"text":"Revenue ↑ 12%"}`)

	final, err := Stream(context.Background(), c, p, secondTurn(r, msg), nil)
	if err != nil || final.Text != "Done." {
		t.Fatalf("second Stream: %+v %v", final, err)
	}
	reqs := f.requests()
	for i, rq := range reqs {
		if rq.Path != "/v1beta/models/gemini-3-pro:streamGenerateContent" || rq.Query != "alt=sse" {
			t.Fatalf("request %d: %s?%s", i, rq.Path, rq.Query)
		}
		if strings.Contains(rq.Query, p.APIKey) || rq.Header.Get("x-goog-api-key") != p.APIKey || rq.Header.Get("Authorization") != "" {
			t.Fatalf("request %d: key placement wrong: query %q headers %v", i, rq.Query, rq.Header)
		}
	}
	first := reqs[0].Body
	jsonEq(t, path(t, first, "systemInstruction"), `{"parts":[{"text":"You edit slide decks."}]}`)
	jsonEq(t, path(t, first, "generationConfig"), `{"maxOutputTokens":2048}`)
	jsonEq(t, path(t, first, "tools"), `[{"functionDeclarations":[
		{"name":"set_title","description":"Set a slide title","parametersJsonSchema":{"type":"object","properties":{"slide":{"type":"integer"},"title":{"type":"string"}},"required":["slide","title"]}},
		{"name":"add_bullet","description":"Add a bullet","parametersJsonSchema":{"type":"object","properties":{"slide":{"type":"integer"},"text":{"type":"string"}},"required":["slide","text"]}}
	]}]`)
	jsonEq(t, path(t, reqs[1].Body, "contents"), `[
		{"role":"user","parts":[{"text":"Retitle slide 1 and add a bullet to slide 9"}]},
		{"role":"model","parts":[
			{"text":"I'll update both slides."},
			{"functionCall":{"id":"fc-a1","name":"set_title","args":{"slide":1,"title":"Q3 \"results\""}},"thoughtSignature":"CiQBVKhc7hXdSIG"},
			{"functionCall":{"id":"fc-b2","name":"add_bullet","args":{"slide":9,"text":"Revenue ↑ 12%"}}}
		]},
		{"role":"user","parts":[
			{"functionResponse":{"id":"fc-a1","name":"set_title","response":{"result":"title set"}}},
			{"functionResponse":{"id":"fc-b2","name":"add_bullet","response":{"error":"slide 9 does not exist"}}}
		]}
	]`)
}

// Older Gemini models send no functionCall id: a local id is made up for the
// caller, but never sent to Gemini.
func TestGoogleSyntheticIDsNotSent(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		s := startSSE(w)
		s.event("", `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"set_title","args":{"slide":1,"title":"x"}}},{"functionCall":{"name":"add_bullet","args":{"slide":2,"text":"y"}}}],"role":"model"},"finishReason":"STOP","index":0}]}`)
	}, googleFinalTurn)
	p := Provider{Kind: KindGoogle, BaseURL: f.URL + "/v1beta/"}
	r := Request{Model: "models/gemini-2.0-flash", Tools: slideTools, Messages: []Message{{Role: "user", Text: "go"}}}
	msg, err := Stream(context.Background(), nil, p, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 2 || msg.ToolCalls[0].ID == "" || msg.ToolCalls[0].ID == msg.ToolCalls[1].ID {
		t.Fatalf("ids %+v", msg.ToolCalls)
	}
	if _, err := Stream(context.Background(), nil, p, secondTurn(r, msg), nil); err != nil {
		t.Fatal(err)
	}
	reqs := f.requests()
	if reqs[0].Path != "/v1beta/models/gemini-2.0-flash:streamGenerateContent" {
		t.Fatalf("path %s", reqs[0].Path)
	}
	jsonEq(t, path(t, reqs[1].Body, "contents.1"), `{"role":"model","parts":[
		{"functionCall":{"name":"set_title","args":{"slide":1,"title":"x"}}},
		{"functionCall":{"name":"add_bullet","args":{"slide":2,"text":"y"}}}]}`)
	jsonEq(t, path(t, reqs[1].Body, "contents.2"), `{"role":"user","parts":[
		{"functionResponse":{"name":"set_title","response":{"result":"title set"}}},
		{"functionResponse":{"name":"add_bullet","response":{"error":"slide 9 does not exist"}}}]}`)
}
