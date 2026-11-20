package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Anthropic Messages API (POST {base}/v1/messages, stream: true), headers
// x-api-key and anthropic-version. The stream is message_start, then per
// content block content_block_start / content_block_delta* /
// content_block_stop, then message_delta (stop_reason) and message_stop;
// ping and error events may appear anywhere. tool_use input arrives as
// input_json_delta partial_json fragments. Tool results go back as
// tool_result blocks (tool_use_id, content, is_error), all of a turn's
// results in one user message. Thinking blocks (thinking + signature_delta,
// or redacted_thinking) are kept in Message.Native and echoed back verbatim.

type anRequest struct {
	Model     string      `json:"model"`
	MaxTokens int         `json:"max_tokens"`
	System    string      `json:"system,omitempty"`
	Messages  []anMessage `json:"messages"`
	Tools     []anTool    `json:"tools,omitempty"`
	Stream    bool        `json:"stream"`
}

type anMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anToolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type anToolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // only plain structs are marshalled here
	}
	return b
}

func buildAnthropic(r Request) anRequest {
	out := anRequest{Model: r.Model, MaxTokens: r.MaxTokens, System: r.System, Stream: true}
	if out.MaxTokens <= 0 {
		out.MaxTokens = DefaultAnthropicMaxTokens
	}
	var pending []any // tool_result blocks waiting to be sent as one user message
	flush := func() {
		if len(pending) > 0 {
			out.Messages = append(out.Messages, anMessage{Role: "user", Content: mustJSON(pending)})
			pending = nil
		}
	}
	for _, m := range r.Messages {
		switch m.Role {
		case "tool":
			pending = append(pending, anToolResultBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Text, IsError: m.IsError})
		case "assistant":
			flush()
			if m.NativeKind == KindAnthropic && len(m.Native) > 0 && json.Valid(m.Native) {
				out.Messages = append(out.Messages, anMessage{Role: "assistant", Content: m.Native})
				continue
			}
			var blocks []any
			if m.Text != "" {
				blocks = append(blocks, anTextBlock{Type: "text", Text: m.Text})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, anToolUseBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: argsOrEmpty(tc.Args)})
			}
			if len(blocks) == 0 {
				continue // the API rejects empty assistant content
			}
			out.Messages = append(out.Messages, anMessage{Role: "assistant", Content: mustJSON(blocks)})
		default:
			flush()
			out.Messages = append(out.Messages, anMessage{Role: "user", Content: mustJSON(m.Text)})
		}
	}
	flush()
	for _, t := range r.Tools {
		out.Tools = append(out.Tools, anTool{Name: t.Name, Description: t.Description, InputSchema: toolSchema(t)})
	}
	return out
}

// anthropicBase strips a trailing /v1 so both https://api.anthropic.com and
// https://api.anthropic.com/v1 work as base URLs.
func anthropicEndpoint(p Provider, path string, q url.Values) (string, error) {
	pp := p
	pp.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	pp.BaseURL = strings.TrimSuffix(pp.BaseURL, "/v1")
	return endpoint(pp, "/v1"+path, q)
}

func setAnthropicAuth(req *http.Request, p Provider) {
	if p.APIKey != "" {
		req.Header.Set("x-api-key", p.APIKey)
	}
	req.Header.Set("anthropic-version", AnthropicVersion)
}

type anBlock struct {
	typ   string
	start json.RawMessage // content_block as received in content_block_start
	id    string
	name  string
	text  strings.Builder // text, thinking, or tool input JSON
	sig   strings.Builder
}

func streamAnthropic(ctx context.Context, c *http.Client, p Provider, r Request, onText func(string)) (Message, error) {
	u, err := anthropicEndpoint(p, "/messages", nil)
	if err != nil {
		return Message{}, err
	}
	req, err := newJSONRequest(ctx, http.MethodPost, u, buildAnthropic(r))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Accept", "text/event-stream")
	setAnthropicAuth(req, p)
	resp, err := do(c, KindAnthropic, req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	var (
		blocks     []*anBlock
		byIndex    = map[int]*anBlock{}
		stopReason string
		stopped    bool
	)
	err = readSSE(resp.Body, func(ev sseEvent) error {
		if strings.TrimSpace(ev.Data) == "" {
			return nil
		}
		var e struct {
			Type         string          `json:"type"`
			Index        int             `json:"index"`
			ContentBlock json.RawMessage `json:"content_block"`
			Delta        struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &e); err != nil {
			return fmt.Errorf("bad stream event: %w", err)
		}
		switch e.Type {
		case "content_block_start":
			var cb struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				Name      string `json:"name"`
				Text      string `json:"text"`
				Thinking  string `json:"thinking"`
				Signature string `json:"signature"`
			}
			if err := json.Unmarshal(e.ContentBlock, &cb); err != nil {
				return fmt.Errorf("bad content block: %w", err)
			}
			b := &anBlock{typ: cb.Type, start: append(json.RawMessage(nil), e.ContentBlock...), id: cb.ID, name: cb.Name}
			switch cb.Type {
			case "text":
				b.text.WriteString(cb.Text)
				if cb.Text != "" {
					onText(cb.Text)
				}
			case "thinking":
				b.text.WriteString(cb.Thinking)
				b.sig.WriteString(cb.Signature)
			}
			byIndex[e.Index] = b
			blocks = append(blocks, b)
		case "content_block_delta":
			b := byIndex[e.Index]
			if b == nil {
				return fmt.Errorf("delta for unknown content block %d", e.Index)
			}
			switch e.Delta.Type {
			case "text_delta":
				b.text.WriteString(e.Delta.Text)
				if e.Delta.Text != "" {
					onText(e.Delta.Text)
				}
			case "input_json_delta":
				b.text.WriteString(e.Delta.PartialJSON)
			case "thinking_delta":
				b.text.WriteString(e.Delta.Thinking)
			case "signature_delta":
				b.sig.WriteString(e.Delta.Signature)
			}
		case "message_delta":
			if e.Delta.StopReason != "" {
				stopReason = e.Delta.StopReason
			}
		case "message_stop":
			stopped = true
			return errStopSSE
		case "error":
			return &APIError{Kind: KindAnthropic, Message: errorMessage([]byte(ev.Data), "error")}
		}
		return nil
	})

	msg := Message{Role: "assistant"}
	var native []json.RawMessage
	var text strings.Builder
	for _, b := range blocks {
		switch b.typ {
		case "text":
			text.WriteString(b.text.String())
			if b.text.Len() > 0 {
				native = append(native, mustJSON(anTextBlock{Type: "text", Text: b.text.String()}))
			}
		case "tool_use":
			args := strings.TrimSpace(b.text.String())
			if args == "" {
				args = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: b.id, Name: b.name, Args: json.RawMessage(args)})
			native = append(native, mustJSON(anToolUseBlock{Type: "tool_use", ID: b.id, Name: b.name, Input: argsOrEmpty(json.RawMessage(args))}))
		case "thinking":
			native = append(native, mustJSON(struct {
				Type      string `json:"type"`
				Thinking  string `json:"thinking"`
				Signature string `json:"signature"`
			}{"thinking", b.text.String(), b.sig.String()}))
		default:
			// redacted_thinking and any other block arrive complete in
			// content_block_start; echo them exactly as received.
			native = append(native, b.start)
		}
	}
	msg.Text = text.String()
	if len(native) > 0 {
		msg.Native = mustJSON(native)
		msg.NativeKind = KindAnthropic
	}
	if err != nil {
		return msg, err
	}
	if !stopped && stopReason == "" {
		return msg, errors.New("stream ended before the model finished")
	}
	switch stopReason {
	case "max_tokens", "model_context_window_exceeded":
		return msg, errors.New("model hit the max token limit before finishing")
	case "refusal":
		return msg, errors.New("the model declined to continue (refusal)")
	}
	return msg, nil
}

func listAnthropic(ctx context.Context, c *http.Client, p Provider) ([]string, error) {
	var ids []string
	after := ""
	for page := 0; page < 50; page++ {
		q := url.Values{"limit": {"1000"}}
		if after != "" {
			q.Set("after_id", after)
		}
		u, err := anthropicEndpoint(p, "/models", q)
		if err != nil {
			return nil, err
		}
		req, err := newJSONRequest(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		setAnthropicAuth(req, p)
		resp, err := do(c, KindAnthropic, req)
		if err != nil {
			return nil, err
		}
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		err = json.NewDecoder(limitBody(resp.Body)).Decode(&out)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("bad model list: %w", err)
		}
		for _, m := range out.Data {
			ids = append(ids, m.ID)
		}
		if !out.HasMore || out.LastID == "" || out.LastID == after {
			break
		}
		after = out.LastID
	}
	return ids, nil
}
