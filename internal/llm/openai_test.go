package llm

import (
	"context"
	"net/http"
	"testing"
)

// openAIToolTurn streams like Chat Completions: role chunk, content deltas,
// then two parallel tool calls whose arguments arrive as interleaved
// fragments keyed by index, finish_reason "tool_calls", [DONE].
func openAIToolTurn(w http.ResponseWriter, _ *http.Request, _ []byte) {
	s := startSSE(w)
	const head = `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1767225600,"model":"gpt-5","choices":[`
	s.event("", head+`{"index":0,"delta":{"role":"assistant","content":"","refusal":null},"logprobs":null,"finish_reason":null}]}`)
	s.event("", head+`{"index":0,"delta":{"content":"I'll update "},"logprobs":null,"finish_reason":null}]}`)
	s.event("", head+`{"index":0,"delta":{"content":"both slides."},"logprobs":null,"finish_reason":null}]}`)
	s.event("", head+`{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_A1","type":"function","function":{"name":"set_title","arguments":""}}]},"logprobs":null,"finish_reason":null}]}`)
	s.event("", head+`{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"slide\": 1, \"ti"}}]},"logprobs":null,"finish_reason":null}]}`)
	s.event("", head+`{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_B2","type":"function","function":{"name":"add_bullet","arguments":""}}]},"logprobs":null,"finish_reason":null}]}`)
	s.event("", head+`{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"slide\":9,"}}]},"logprobs":null,"finish_reason":null}]}`)
	s.event("", head+`{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"tle\": \"Q3 \\\"results\\\"\"}"}}]},"logprobs":null,"finish_reason":null}]}`)
	s.event("", head+`{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"text\":\"Revenue ↑ 12%\"}"}}]},"logprobs":null,"finish_reason":null}]}`)
	s.event("", head+`{"index":0,"delta":{},"logprobs":null,"finish_reason":"tool_calls"}]}`)
	s.event("", `[DONE]`)
}

func openAIFinalTurn(w http.ResponseWriter, _ *http.Request, _ []byte) {
	s := startSSE(w)
	const head = `{"id":"chatcmpl-2","object":"chat.completion.chunk","created":1767225601,"model":"gpt-5","choices":[`
	s.event("", head+`{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
	s.event("", head+`{"index":0,"delta":{"content":"Done."},"finish_reason":null}]}`)
	s.event("", head+`{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	s.event("", `[DONE]`)
}

func TestOpenAIToolRoundTrip(t *testing.T) {
	f := newFake(t, openAIToolTurn, openAIFinalTurn)
	p := Provider{Kind: KindOpenAI, BaseURL: f.URL + "/v1", APIKey: "sk-proj-test0123456789abcdefXYZ"}
	c, err := NewClient(p)
	if err != nil {
		t.Fatal(err)
	}
	r := Request{Model: "gpt-5", System: "You edit slide decks.", Tools: slideTools, MaxTokens: 4000,
		Messages: []Message{{Role: "user", Text: "Retitle slide 1 and add a bullet to slide 9"}}}
	var sink textSink
	msg, err := Stream(context.Background(), c, p, r, sink.add)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if sink.joined() != "I'll update both slides." || sink.count() != 2 || msg.Text != sink.joined() {
		t.Fatalf("text %q / %q (%d deltas)", msg.Text, sink.joined(), sink.count())
	}
	if len(msg.ToolCalls) != 2 || msg.ToolCalls[0].ID != "call_A1" || msg.ToolCalls[0].Name != "set_title" ||
		msg.ToolCalls[1].ID != "call_B2" || msg.ToolCalls[1].Name != "add_bullet" {
		t.Fatalf("tool calls %+v", msg.ToolCalls)
	}
	jsonEq(t, msg.ToolCalls[0].Args, `{"slide":1,"title":"Q3 \"results\""}`)
	jsonEq(t, msg.ToolCalls[1].Args, `{"slide":9,"text":"Revenue ↑ 12%"}`)

	if _, err := Stream(context.Background(), c, p, secondTurn(r, msg), nil); err != nil {
		t.Fatalf("second Stream: %v", err)
	}
	reqs := f.requests()
	if len(reqs) != 2 {
		t.Fatalf("want 2 requests, got %d", len(reqs))
	}
	for i, rq := range reqs {
		if rq.Path != "/v1/chat/completions" || rq.Header.Get("Authorization") != "Bearer "+p.APIKey {
			t.Fatalf("request %d: path %s auth %q", i, rq.Path, rq.Header.Get("Authorization"))
		}
	}
	first := reqs[0].Body
	jsonEq(t, path(t, first, "stream"), `true`)
	jsonEq(t, path(t, first, "max_completion_tokens"), `4000`)
	jsonEq(t, path(t, first, "tools.1"), `{"type":"function","function":{"name":"add_bullet","description":"Add a bullet","parameters":{"type":"object","properties":{"slide":{"type":"integer"},"text":{"type":"string"}},"required":["slide","text"]}}}`)
	jsonEq(t, path(t, reqs[1].Body, "messages"), `[
		{"role":"system","content":"You edit slide decks."},
		{"role":"user","content":"Retitle slide 1 and add a bullet to slide 9"},
		{"role":"assistant","content":"I'll update both slides.","tool_calls":[
			{"id":"call_A1","type":"function","function":{"name":"set_title","arguments":"{\"slide\": 1, \"title\": \"Q3 \\\"results\\\"\"}"}},
			{"id":"call_B2","type":"function","function":{"name":"add_bullet","arguments":"{\"slide\":9,\"text\":\"Revenue ↑ 12%\"}"}}
		]},
		{"role":"tool","tool_call_id":"call_A1","content":"title set"},
		{"role":"tool","tool_call_id":"call_B2","content":"Error: slide 9 does not exist"}
	]`)
}

// A local compatible server with no key: no Authorization header, and the
// classic max_tokens field.
func TestCompatibleNoKey(t *testing.T) {
	f := newFake(t, openAIFinalTurn)
	p := Provider{Kind: KindCompatible, BaseURL: f.URL + "/v1"}
	msg, err := Stream(context.Background(), nil, p, Request{Model: "llama3", MaxTokens: 100, Messages: []Message{{Role: "user", Text: "hi"}}}, nil)
	if err != nil || msg.Text != "Done." {
		t.Fatalf("msg %+v err %v", msg, err)
	}
	rq := f.requests()[0]
	if _, ok := rq.Header["Authorization"]; ok {
		t.Fatal("Authorization header sent with an empty key")
	}
	jsonEq(t, path(t, rq.Body, "max_tokens"), `100`)
}
