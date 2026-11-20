package llm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// captured is one request seen by a fake provider.
type captured struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   []byte
}

// fake is an httptest server that answers each request with the next handler
// in turns and records every request.
type fake struct {
	*httptest.Server
	mu    sync.Mutex
	reqs  []captured
	turns []func(w http.ResponseWriter, r *http.Request, body []byte)
}

func newFake(t *testing.T, turns ...func(w http.ResponseWriter, r *http.Request, body []byte)) *fake {
	t.Helper()
	f := &fake{turns: turns}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		n := len(f.reqs)
		f.reqs = append(f.reqs, captured{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: body})
		f.mu.Unlock()
		if n >= len(f.turns) {
			http.Error(w, "unexpected request", http.StatusTeapot)
			return
		}
		f.turns[n](w, r, body)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fake) requests() []captured {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]captured(nil), f.reqs...)
}

// sse writes server-sent events, flushing after each so the client sees them
// one by one as from a real provider.
type sse struct {
	w http.ResponseWriter
	f http.Flusher
}

func startSSE(w http.ResponseWriter) *sse {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	s := &sse{w: w, f: w.(http.Flusher)}
	s.f.Flush()
	return s
}

// event writes "event: name" (if name != "") and one data line.
func (s *sse) event(name, data string) {
	if name != "" {
		fmt.Fprintf(s.w, "event: %s\n", name)
	}
	fmt.Fprintf(s.w, "data: %s\n\n", data)
	s.f.Flush()
}

func (s *sse) raw(text string) {
	io.WriteString(s.w, text)
	s.f.Flush()
}

// jsonEq compares two JSON documents structurally.
func jsonEq(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("got invalid JSON %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad want JSON %q: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("JSON mismatch\n got: %s\nwant: %s", got, want)
	}
}

// path extracts a sub-document from a JSON body by a dotted path of object
// keys and array indexes ("messages.1.content").
func path(t *testing.T, body []byte, p string) json.RawMessage {
	t.Helper()
	cur := json.RawMessage(body)
	if p == "" {
		return cur
	}
	for _, key := range strings.Split(p, ".") {
		var idx int
		if _, err := fmt.Sscanf(key, "%d", &idx); err == nil && fmt.Sprint(idx) == key {
			var arr []json.RawMessage
			if err := json.Unmarshal(cur, &arr); err != nil || idx >= len(arr) {
				t.Fatalf("path %s: %s is not an index into %s", p, key, cur)
			}
			cur = arr[idx]
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(cur, &obj); err != nil {
			t.Fatalf("path %s: %s is not an object: %s", p, key, cur)
		}
		v, ok := obj[key]
		if !ok {
			t.Fatalf("path %s: key %s missing in %s", p, key, cur)
		}
		cur = v
	}
	return cur
}

// textSink collects onText deltas.
type textSink struct {
	mu     sync.Mutex
	deltas []string
}

func (s *textSink) add(d string) {
	s.mu.Lock()
	s.deltas = append(s.deltas, d)
	s.mu.Unlock()
}

func (s *textSink) joined() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.deltas, "")
}

func (s *textSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deltas)
}

// slideTools is the tool list used by the wire-format tests.
var slideTools = []Tool{
	{Name: "set_title", Description: "Set a slide title", Schema: json.RawMessage(`{"type":"object","properties":{"slide":{"type":"integer"},"title":{"type":"string"}},"required":["slide","title"]}`)},
	{Name: "add_bullet", Description: "Add a bullet", Schema: json.RawMessage(`{"type":"object","properties":{"slide":{"type":"integer"},"text":{"type":"string"}},"required":["slide","text"]}`)},
}

// secondTurn builds the follow-up request after the assistant message: the
// first tool succeeded, the second failed.
func secondTurn(r Request, assistant Message) Request {
	r2 := r
	r2.Messages = append(append([]Message(nil), r.Messages...), assistant,
		Message{Role: "tool", ToolCallID: assistant.ToolCalls[0].ID, ToolName: assistant.ToolCalls[0].Name, Text: "title set"},
		Message{Role: "tool", ToolCallID: assistant.ToolCalls[1].ID, ToolName: assistant.ToolCalls[1].Name, Text: "slide 9 does not exist", IsError: true},
	)
	return r2
}
