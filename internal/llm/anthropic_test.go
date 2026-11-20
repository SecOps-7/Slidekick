package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// anthropicToolTurn streams like the Messages API does for adaptive thinking,
// text, then two parallel tool_use blocks whose input arrives as
// input_json_delta fragments.
func anthropicToolTurn(w http.ResponseWriter, _ *http.Request, _ []byte) {
	s := startSSE(w)
	s.event("message_start", `{"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":120,"output_tokens":1}}}`)
	s.event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`)
	s.event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Two edits needed."}}`)
	s.event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"EqQBCgIYAhIM1gbcDa9GJwZA2b3h"}}`)
	s.event("content_block_stop", `{"type":"content_block_stop","index":0}`)
	s.event("ping", `{"type":"ping"}`)
	s.event("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`)
	s.event("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"I'll update "}}`)
	s.event("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"both slides."}}`)
	s.event("content_block_stop", `{"type":"content_block_stop","index":1}`)
	s.event("content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_01A","name":"set_title","input":{}}}`)
	s.event("content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":""}}`)
	s.event("content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"slide\": 1, \"ti"}}`)
	s.event("content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"tle\": \"Q3 \\\"res"}}`)
	s.event("content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"ults\\\"\"}"}}`)
	s.event("content_block_stop", `{"type":"content_block_stop","index":2}`)
	s.event("content_block_start", `{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_01B","name":"add_bullet","input":{}}}`)
	s.event("content_block_delta", `{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"slide\":9,"}}`)
	s.event("content_block_delta", `{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"\"text\":\"Revenue ↑ 12%\"}"}}`)
	s.event("content_block_stop", `{"type":"content_block_stop","index":3}`)
	s.event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":89}}`)
	s.event("message_stop", `{"type":"message_stop"}`)
}

func anthropicFinalTurn(w http.ResponseWriter, _ *http.Request, _ []byte) {
	s := startSSE(w)
	s.event("message_start", `{"type":"message_start","message":{"id":"msg_02","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":300,"output_tokens":1}}}`)
	s.event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	s.event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Slide 1 is retitled; slide 9 does not exist."}}`)
	s.event("content_block_stop", `{"type":"content_block_stop","index":0}`)
	s.event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":15}}`)
	s.event("message_stop", `{"type":"message_stop"}`)
}

func TestAnthropicToolRoundTrip(t *testing.T) {
	f := newFake(t, anthropicToolTurn, anthropicFinalTurn)
	p := Provider{Kind: KindAnthropic, BaseURL: f.URL, APIKey: "sk-ant-test-0123456789abcdef"}
	c, err := NewClient(p)
	if err != nil {
		t.Fatal(err)
	}
	r := Request{Model: "claude-opus-5-5", System: "You edit slide decks.", Tools: slideTools,
		Messages: []Message{{Role: "user", Text: "Retitle slide 1 and add a bullet to slide 9"}}}

	var sink textSink
	msg, err := Stream(context.Background(), c, p, r, sink.add)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if sink.count() != 2 || sink.joined() != "I'll update both slides." || msg.Text != "I'll update both slides." {
		t.Fatalf("text deltas %q (%d), msg.Text %q", sink.joined(), sink.count(), msg.Text)
	}
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("want 2 tool calls, got %+v", msg.ToolCalls)
	}
	if tc := msg.ToolCalls[0]; tc.ID != "toolu_01A" || tc.Name != "set_title" {
		t.Fatalf("call 0 = %+v", tc)
	}
	jsonEq(t, msg.ToolCalls[0].Args, `{"slide":1,"title":"Q3 \"results\""}`)
	if tc := msg.ToolCalls[1]; tc.ID != "toolu_01B" || tc.Name != "add_bullet" {
		t.Fatalf("call 1 = %+v", tc)
	}
	jsonEq(t, msg.ToolCalls[1].Args, `{"slide":9,"text":"Revenue ↑ 12%"}`)
	if msg.NativeKind != KindAnthropic {
		t.Fatalf("NativeKind = %q", msg.NativeKind)
	}

	// Second turn: results go back as tool_result blocks in one user message,
	// and the assistant turn is replayed with its thinking block intact.
	if _, err := Stream(context.Background(), c, p, secondTurn(r, msg), nil); err != nil {
		t.Fatalf("second Stream: %v", err)
	}
	reqs := f.requests()
	if len(reqs) != 2 {
		t.Fatalf("want 2 requests, got %d", len(reqs))
	}
	for i, rq := range reqs {
		if rq.Method != http.MethodPost || rq.Path != "/v1/messages" {
			t.Fatalf("request %d: %s %s", i, rq.Method, rq.Path)
		}
		if rq.Header.Get("x-api-key") != p.APIKey || rq.Header.Get("anthropic-version") != AnthropicVersion {
			t.Fatalf("request %d: auth headers %v", i, rq.Header)
		}
		if rq.Header.Get("Authorization") != "" {
			t.Fatalf("request %d: unexpected Authorization header", i)
		}
	}
	first := reqs[0].Body
	jsonEq(t, path(t, first, "stream"), `true`)
	jsonEq(t, path(t, first, "model"), `"claude-opus-5-5"`)
	jsonEq(t, path(t, first, "system"), `"You edit slide decks."`)
	jsonEq(t, path(t, first, "max_tokens"), `32000`)
	jsonEq(t, path(t, first, "tools.0"), `{"name":"set_title","description":"Set a slide title","input_schema":{"type":"object","properties":{"slide":{"type":"integer"},"title":{"type":"string"}},"required":["slide","title"]}}`)
	jsonEq(t, path(t, first, "messages"), `[{"role":"user","content":"Retitle slide 1 and add a bullet to slide 9"}]`)

	second := reqs[1].Body
	jsonEq(t, path(t, second, "messages"), `[
		{"role":"user","content":"Retitle slide 1 and add a bullet to slide 9"},
		{"role":"assistant","content":[
			{"type":"thinking","thinking":"Two edits needed.","signature":"EqQBCgIYAhIM1gbcDa9GJwZA2b3h"},
			{"type":"text","text":"I'll update both slides."},
			{"type":"tool_use","id":"toolu_01A","name":"set_title","input":{"slide":1,"title":"Q3 \"results\""}},
			{"type":"tool_use","id":"toolu_01B","name":"add_bullet","input":{"slide":9,"text":"Revenue ↑ 12%"}}
		]},
		{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"toolu_01A","content":"title set"},
			{"type":"tool_result","tool_use_id":"toolu_01B","content":"slide 9 does not exist","is_error":true}
		]}
	]`)
}

// Without Native (e.g. a turn produced by another provider) the assistant
// turn is rebuilt from Text and ToolCalls.
func TestAnthropicRebuildsForeignAssistantTurn(t *testing.T) {
	r := Request{Model: "m", Messages: []Message{
		{Role: "user", Text: "hi"},
		{Role: "assistant", Text: "", ToolCalls: []ToolCall{{ID: "call_x", Name: "set_title", Args: json.RawMessage(`{"slide":2}`)}},
			Native: json.RawMessage(`[{"type":"text","text":"other"}]`), NativeKind: KindGoogle},
		{Role: "tool", ToolCallID: "call_x", ToolName: "set_title", Text: "ok"},
		{Role: "user", Text: "thanks"},
	}}
	b, _ := json.Marshal(buildAnthropic(r))
	jsonEq(t, path(t, b, "messages"), `[
		{"role":"user","content":"hi"},
		{"role":"assistant","content":[{"type":"tool_use","id":"call_x","name":"set_title","input":{"slide":2}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_x","content":"ok"}]},
		{"role":"user","content":"thanks"}
	]`)
}

func TestAnthropicRedactedThinkingEchoedVerbatim(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		s := startSSE(w)
		s.event("message_start", `{"type":"message_start","message":{"id":"msg_r","type":"message","role":"assistant","content":[]}}`)
		s.event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"EmwKAhgBEgy3va3pzix"}}`)
		s.event("content_block_stop", `{"type":"content_block_stop","index":0}`)
		s.event("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_R","name":"set_title","input":{}}}`)
		s.event("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"slide\":1,\"title\":\"x\"}"}}`)
		s.event("content_block_stop", `{"type":"content_block_stop","index":1}`)
		s.event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`)
		s.event("message_stop", `{"type":"message_stop"}`)
	})
	p := Provider{Kind: KindAnthropic, BaseURL: f.URL + "/v1"}
	msg, err := Stream(context.Background(), nil, p, Request{Model: "m", Messages: []Message{{Role: "user", Text: "go"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, msg.Native, `[{"type":"redacted_thinking","data":"EmwKAhgBEgy3va3pzix"},{"type":"tool_use","id":"toolu_R","name":"set_title","input":{"slide":1,"title":"x"}}]`)
	if got := f.requests()[0].Path; got != "/v1/messages" {
		t.Fatalf("base URL with /v1 gave path %q", got)
	}
}

func TestAnthropicInvalidToolJSON(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		s := startSSE(w)
		s.event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_X","name":"set_title","input":{}}}`)
		s.event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"slide\":1,\"title\":\"cut"}}`)
		s.event("content_block_stop", `{"type":"content_block_stop","index":0}`)
		s.event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`)
		s.event("message_stop", `{"type":"message_stop"}`)
	})
	p := Provider{Kind: KindAnthropic, BaseURL: f.URL}
	_, err := Stream(context.Background(), nil, p, Request{Model: "m", Messages: []Message{{Role: "user", Text: "go"}}}, nil)
	if !errors.Is(err, ErrInvalidToolArgs) {
		t.Fatalf("want ErrInvalidToolArgs for truncated tool JSON, got %v", err)
	}
}
