package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// OpenAI Chat Completions (POST {base}/chat/completions, stream: true).
// Tool-call deltas arrive as choices[0].delta.tool_calls[] entries keyed by
// index; the first fragment of each carries id and function.name, later ones
// append to function.arguments. finish_reason "tool_calls" ends a tool turn.
// The stream ends with "data: [DONE]".

type oaTool struct {
	Type     string     `json:"type"`
	Function oaFunction `json:"function"`
}

type oaFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    *string      `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaRequest struct {
	Model               string      `json:"model"`
	Messages            []oaMessage `json:"messages"`
	Tools               []oaTool    `json:"tools,omitempty"`
	Stream              bool        `json:"stream"`
	MaxCompletionTokens int         `json:"max_completion_tokens,omitempty"`
	MaxTokens           int         `json:"max_tokens,omitempty"`
}

func strPtr(s string) *string { return &s }

func buildOpenAI(p Provider, r Request) oaRequest {
	out := oaRequest{Model: r.Model, Stream: true}
	if r.MaxTokens > 0 {
		if p.Kind == KindOpenAI {
			out.MaxCompletionTokens = r.MaxTokens // max_tokens is deprecated on OpenAI
		} else {
			out.MaxTokens = r.MaxTokens // what most compatible servers understand
		}
	}
	if r.System != "" {
		out.Messages = append(out.Messages, oaMessage{Role: "system", Content: strPtr(r.System)})
	}
	for _, m := range r.Messages {
		switch m.Role {
		case "assistant":
			om := oaMessage{Role: "assistant"}
			if m.Text != "" || len(m.ToolCalls) == 0 {
				om.Content = strPtr(m.Text)
			}
			for _, tc := range m.ToolCalls {
				c := oaToolCall{ID: tc.ID, Type: "function"}
				c.Function.Name = tc.Name
				c.Function.Arguments = string(argsOrEmpty(tc.Args))
				om.ToolCalls = append(om.ToolCalls, c)
			}
			out.Messages = append(out.Messages, om)
		case "tool":
			text := m.Text
			if m.IsError {
				text = "Error: " + text
			}
			out.Messages = append(out.Messages, oaMessage{Role: "tool", ToolCallID: m.ToolCallID, Content: strPtr(text)})
		default:
			out.Messages = append(out.Messages, oaMessage{Role: "user", Content: strPtr(m.Text)})
		}
	}
	for _, t := range r.Tools {
		out.Tools = append(out.Tools, oaTool{Type: "function", Function: oaFunction{Name: t.Name, Description: t.Description, Parameters: toolSchema(t)}})
	}
	return out
}

func setOpenAIAuth(req *http.Request, p Provider) {
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
}

type oaChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content   *string `json:"content"`
			Refusal   *string `json:"refusal"`
			ToolCalls []struct {
				Index    *int   `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Error json.RawMessage `json:"error"`
}

func streamOpenAI(ctx context.Context, c *http.Client, p Provider, r Request, onText func(string)) (Message, error) {
	u, err := endpoint(p, "/chat/completions", nil)
	if err != nil {
		return Message{}, err
	}
	req, err := newJSONRequest(ctx, http.MethodPost, u, buildOpenAI(p, r))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Accept", "text/event-stream")
	setOpenAIAuth(req, p)
	resp, err := do(c, p.Kind, req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	type acc struct {
		id, name string
		args     strings.Builder
	}
	var (
		text    strings.Builder
		refusal strings.Builder
		calls   []*acc
		byIndex = map[int]*acc{}
		finish  string
		done    bool
	)
	err = readSSE(resp.Body, func(ev sseEvent) error {
		data := strings.TrimSpace(ev.Data)
		if data == "[DONE]" {
			done = true
			return errStopSSE
		}
		if data == "" {
			return nil
		}
		var ch oaChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return fmt.Errorf("bad stream chunk: %w", err)
		}
		if len(ch.Error) > 0 && string(ch.Error) != "null" {
			return &APIError{Kind: p.Kind, Message: errorMessage([]byte(data), "error")}
		}
		for _, choice := range ch.Choices {
			if choice.Index != 0 {
				continue
			}
			d := choice.Delta
			if d.Content != nil && *d.Content != "" {
				text.WriteString(*d.Content)
				onText(*d.Content)
			}
			if d.Refusal != nil {
				refusal.WriteString(*d.Refusal)
			}
			for _, tc := range d.ToolCalls {
				var a *acc
				switch {
				case tc.Index != nil:
					a = byIndex[*tc.Index]
					if a == nil {
						a = &acc{}
						byIndex[*tc.Index] = a
						calls = append(calls, a)
					}
				case tc.ID != "" || len(calls) == 0:
					// Some compatible servers omit index: a new id starts a new call.
					if len(calls) > 0 && (calls[len(calls)-1].id == tc.ID) {
						a = calls[len(calls)-1]
					} else {
						a = &acc{}
						calls = append(calls, a)
					}
				default:
					a = calls[len(calls)-1]
				}
				if tc.ID != "" {
					a.id = tc.ID
				}
				if tc.Function.Name != "" {
					a.name = tc.Function.Name
				}
				a.args.WriteString(tc.Function.Arguments)
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finish = *choice.FinishReason
			}
		}
		return nil
	})
	msg := Message{Role: "assistant", Text: text.String()}
	for i, a := range calls {
		id := a.id
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: id, Name: a.name, Args: json.RawMessage(a.args.String())})
	}
	if err != nil {
		return msg, err
	}
	if finish == "" && !done {
		return msg, errors.New("stream ended before the model finished")
	}
	switch finish {
	case "length":
		return msg, errors.New("model hit the max token limit before finishing")
	case "content_filter":
		return msg, errors.New("response blocked by the provider's content filter")
	}
	if refusal.Len() > 0 && msg.Text == "" {
		msg.Text = refusal.String()
		onText(msg.Text)
	}
	return msg, nil
}

func listOpenAI(ctx context.Context, c *http.Client, p Provider) ([]string, error) {
	u, err := endpoint(p, "/models", nil)
	if err != nil {
		return nil, err
	}
	req, err := newJSONRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	setOpenAIAuth(req, p)
	resp, err := do(c, p.Kind, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(limitBody(resp.Body)).Decode(&out); err != nil {
		return nil, fmt.Errorf("bad model list: %w", err)
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}
