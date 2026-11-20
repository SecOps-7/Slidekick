// Package llm is a small, dependency-free client for the chat-completion
// streaming APIs of OpenAI (and OpenAI-compatible servers), Anthropic and
// Google Gemini, with tool calling. It sends one model turn per Stream call;
// the caller owns the conversation loop and executes the tools.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Kind names a wire format.
type Kind string

const (
	KindOpenAI     Kind = "openai"
	KindAnthropic  Kind = "anthropic"
	KindGoogle     Kind = "google"
	KindCompatible Kind = "openai-compatible"
)

// Provider is one configured endpoint.
type Provider struct {
	Kind    Kind
	BaseURL string // e.g. https://api.openai.com/v1, https://api.anthropic.com, https://generativelanguage.googleapis.com/v1beta, http://172.16.5.9:11434/v1
	APIKey  string // may be empty for local compatible endpoints
}

// Tool is a function the model may call.
type Tool struct {
	Name, Description string
	Schema            json.RawMessage // JSON Schema object for the input
}

// ToolCall is one function call made by the model.
type ToolCall struct {
	ID, Name string
	Args     json.RawMessage
	// Meta carries provider extras that must be echoed back unchanged on the
	// next request (MetaGoogleThoughtSignature, MetaGoogleSyntheticID).
	Meta map[string]string
}

// Meta keys set by Stream. Keep them when storing the conversation.
const (
	// MetaGoogleThoughtSignature is the Gemini thoughtSignature attached to a
	// functionCall part; it is sent back on the same part.
	MetaGoogleThoughtSignature = "google.thoughtSignature"
	// MetaGoogleSyntheticID is "1" when Gemini returned no call id and the
	// ToolCall.ID was made up locally; it is then not sent back to Gemini.
	MetaGoogleSyntheticID = "google.syntheticID"
)

// Message is one conversation turn.
type Message struct {
	Role       string // "user", "assistant", "tool"
	Text       string
	ToolCalls  []ToolCall // assistant turns
	ToolCallID string     // tool turns
	ToolName   string     // tool turns
	IsError    bool       // tool turns

	// Native is the provider's own encoding of an assistant turn, recorded by
	// Stream when the provider needs it echoed back verbatim. For Anthropic it
	// is the response's content-block array in the order received, including
	// thinking / redacted_thinking blocks and their signatures (the API
	// rejects tool-use follow-ups whose thinking blocks were dropped or
	// altered). It is replayed only to a provider of NativeKind; any other
	// provider rebuilds the turn from Text and ToolCalls. Store it with the
	// message and never edit it.
	Native     json.RawMessage
	NativeKind Kind
}

// Request is one model turn.
type Request struct {
	Model, System string
	Messages      []Message
	Tools         []Tool
	MaxTokens     int // 0 = provider default (Anthropic requires a value; DefaultAnthropicMaxTokens is used)
}

// DefaultAnthropicMaxTokens is sent when Request.MaxTokens is 0 for Anthropic,
// whose API requires max_tokens.
const DefaultAnthropicMaxTokens = 32000

// AnthropicVersion is the anthropic-version header value.
const AnthropicVersion = "2026-10-21"

// APIError is a non-2xx answer (or an in-stream error event) from a provider.
// Message is already redacted and truncated.
type APIError struct {
	Kind    Kind
	Status  int // HTTP status; 0 for an error event inside a 200 stream
	Message string
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("%s: stream error: %s", e.Kind, e.Message)
	}
	return fmt.Sprintf("%s: HTTP %d: %s", e.Kind, e.Status, e.Message)
}

// ErrInvalidToolArgs is wrapped by the error Stream returns when a tool
// call's accumulated arguments are not valid JSON.
var ErrInvalidToolArgs = errors.New("tool call arguments are not valid JSON")

const (
	maxErrorMessage = 300
	maxErrorBody    = 64 << 10
)

// Stream sends one model turn, calls onText for each text delta as it
// arrives, and returns the full assistant message (text + tool calls with
// complete, valid JSON args). It honours ctx cancellation: the request body
// is closed as soon as ctx is done and ctx.Err() is returned. Every other
// returned error has the API key redacted.
func Stream(ctx context.Context, c *http.Client, p Provider, r Request, onText func(string)) (Message, error) {
	if onText == nil {
		onText = func(string) {}
	}
	if c == nil {
		var err error
		if c, err = NewClient(p); err != nil {
			return Message{}, p.redactErr(err)
		}
	}
	if strings.TrimSpace(r.Model) == "" {
		return Message{}, fmt.Errorf("llm: model is required")
	}
	var (
		msg Message
		err error
	)
	switch p.Kind {
	case KindOpenAI, KindCompatible:
		msg, err = streamOpenAI(ctx, c, p, r, onText)
	case KindAnthropic:
		msg, err = streamAnthropic(ctx, c, p, r, onText)
	case KindGoogle:
		msg, err = streamGoogle(ctx, c, p, r, onText)
	default:
		return Message{}, fmt.Errorf("llm: unknown provider kind %q", p.Kind)
	}
	if err != nil {
		if ce := ctx.Err(); ce != nil {
			return msg, ce
		}
		return msg, p.redactErr(err)
	}
	if err := checkArgs(msg.ToolCalls); err != nil {
		return msg, p.redactErr(err)
	}
	return msg, nil
}

// ListModels asks the provider for its model ids, sorted.
func ListModels(ctx context.Context, c *http.Client, p Provider) ([]string, error) {
	if c == nil {
		var err error
		if c, err = NewClient(p); err != nil {
			return nil, p.redactErr(err)
		}
	}
	var (
		ids []string
		err error
	)
	switch p.Kind {
	case KindOpenAI, KindCompatible:
		ids, err = listOpenAI(ctx, c, p)
	case KindAnthropic:
		ids, err = listAnthropic(ctx, c, p)
	case KindGoogle:
		ids, err = listGoogle(ctx, c, p)
	default:
		return nil, fmt.Errorf("llm: unknown provider kind %q", p.Kind)
	}
	if err != nil {
		if ce := ctx.Err(); ce != nil {
			return nil, ce
		}
		return nil, p.redactErr(err)
	}
	sort.Strings(ids)
	out := ids[:0]
	for i, id := range ids {
		if id != "" && (i == 0 || id != ids[i-1]) {
			out = append(out, id)
		}
	}
	return out, nil
}

func checkArgs(calls []ToolCall) error {
	for i := range calls {
		a := strings.TrimSpace(string(calls[i].Args))
		if a == "" {
			calls[i].Args = json.RawMessage("{}")
			continue
		}
		if !json.Valid([]byte(a)) {
			return fmt.Errorf("tool call %q (%s): %w", calls[i].Name, calls[i].ID, ErrInvalidToolArgs)
		}
		calls[i].Args = json.RawMessage(a)
	}
	return nil
}

// redactErr turns any error into one whose text carries no part of the key.
func (p Provider) redactErr(err error) error {
	if err == nil {
		return nil
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return &APIError{Kind: ae.Kind, Status: ae.Status, Message: truncate(Redact(ae.Message, p.APIKey), maxErrorMessage)}
	}
	re := &redactedError{msg: truncate(Redact(err.Error(), p.APIKey), 2*maxErrorMessage)}
	if errors.Is(err, ErrInvalidToolArgs) {
		re.sentinel = ErrInvalidToolArgs
	}
	return re
}

// redactedError carries only redacted text; it unwraps to a fixed sentinel
// (never to the original error, whose text may hold the key).
type redactedError struct {
	msg      string
	sentinel error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.sentinel }

// Redact removes the API key, and any substring of it of 8 or more
// characters (providers echo keys partially, e.g. "sk-proj-abcdefgh...wxyz"),
// from s.
func Redact(s, key string) string {
	if key == "" || s == "" {
		return s
	}
	const mark = "[REDACTED]"
	if len(key) >= 4 {
		s = strings.ReplaceAll(s, key, mark)
	}
	const minRun = 8
	if len(key) < minRun {
		return s
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		j := i
		// Substring-of-key is monotone: if s[i:j+1] is in key, so is s[i:j].
		for j < len(s) && strings.Contains(key, s[i:j+1]) {
			j++
		}
		if j-i >= minRun {
			b.WriteString(mark)
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	// Do not cut a UTF-8 sequence in half.
	cut := n
	for cut > 0 && cut < len(s) && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "..."
}

// endpoint joins the base URL's path with extra path segments.
func endpoint(p Provider, path string, query url.Values) (string, error) {
	u, err := ValidateBaseURL(p.BaseURL)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath = ""
	if query != nil {
		u.RawQuery = query.Encode()
	}
	return u.String(), nil
}

func newJSONRequest(ctx context.Context, method, u string, body any) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// do sends req and turns a non-2xx answer into an *APIError.
func do(c *http.Client, kind Kind, req *http.Request) (*http.Response, error) {
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, &APIError{Kind: kind, Status: resp.StatusCode, Message: errorMessage(b, resp.Status)}
	}
	return resp, nil
}

// errorMessage pulls the human message out of a provider error body:
// OpenAI {"error":{"message"}}, Anthropic {"type":"error","error":{"type","message"}},
// Google {"error":{"code","message","status"}}.
func errorMessage(body []byte, fallback string) string {
	var e struct {
		Error json.RawMessage `json:"error"`
		Msg   string          `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil {
		var inner struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Status  string `json:"status"`
		}
		if json.Unmarshal(e.Error, &inner) == nil && inner.Message != "" {
			kind := inner.Type
			if kind == "" {
				kind = inner.Status
			}
			if kind != "" {
				return kind + ": " + inner.Message
			}
			return inner.Message
		}
		var s string
		if json.Unmarshal(e.Error, &s) == nil && s != "" {
			return s
		}
		if e.Msg != "" {
			return e.Msg
		}
	}
	t := strings.Join(strings.Fields(string(body)), " ")
	if t == "" {
		return fallback
	}
	return t
}

// limitBody bounds a non-streaming JSON answer (model lists).
func limitBody(r io.Reader) io.Reader { return io.LimitReader(r, 16<<20) }

// emptyObjectSchema is used for tools that declare no schema.
var emptyObjectSchema = json.RawMessage(`{"type":"object","properties":{}}`)

func toolSchema(t Tool) json.RawMessage {
	if len(strings.TrimSpace(string(t.Schema))) == 0 {
		return emptyObjectSchema
	}
	return t.Schema
}

// argsOrEmpty returns a ToolCall's args as a JSON value usable in a request.
func argsOrEmpty(a json.RawMessage) json.RawMessage {
	if len(strings.TrimSpace(string(a))) == 0 || !json.Valid(a) {
		return json.RawMessage("{}")
	}
	return a
}
