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

// Google Gemini API (POST {base}/models/{model}:streamGenerateContent?alt=sse,
// key in the x-goog-api-key header). Each SSE event is a whole
// GenerateContentResponse; text arrives as text parts spread over events,
// function calls as complete functionCall parts ({id, name, args}). Newer
// models attach a thoughtSignature to a functionCall part (with parallel
// calls only the first one); it must be sent back on that same part, in the
// same order, or the follow-up request is rejected. Tool results go back as
// functionResponse parts ({id, name, response}) in one "user" content.
// Parts flagged "thought": true are reasoning summaries, not answer text.

type gmPart struct {
	Text             string          `json:"text,omitempty"`
	Thought          bool            `json:"thought,omitempty"`
	ThoughtSignature string          `json:"thoughtSignature,omitempty"`
	FunctionCall     *gmFuncCall     `json:"functionCall,omitempty"`
	FunctionResponse *gmFuncResponse `json:"functionResponse,omitempty"`
}

type gmFuncCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type gmFuncResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type gmContent struct {
	Role  string   `json:"role,omitempty"`
	Parts []gmPart `json:"parts"`
}

type gmFuncDecl struct {
	Name                 string          `json:"name"`
	Description          string          `json:"description"`
	ParametersJSONSchema json.RawMessage `json:"parametersJsonSchema"`
}

type gmTool struct {
	FunctionDeclarations []gmFuncDecl `json:"functionDeclarations"`
}

type gmRequest struct {
	Contents          []gmContent `json:"contents"`
	SystemInstruction *gmContent  `json:"systemInstruction,omitempty"`
	Tools             []gmTool    `json:"tools,omitempty"`
	GenerationConfig  *struct {
		MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
	} `json:"generationConfig,omitempty"`
}

func buildGoogle(r Request) gmRequest {
	var out gmRequest
	if r.System != "" {
		out.SystemInstruction = &gmContent{Parts: []gmPart{{Text: r.System}}}
	}
	if r.MaxTokens > 0 {
		out.GenerationConfig = &struct {
			MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
		}{r.MaxTokens}
	}
	var pending []gmPart
	flush := func() {
		if len(pending) > 0 {
			out.Contents = append(out.Contents, gmContent{Role: "user", Parts: pending})
			pending = nil
		}
	}
	for _, m := range r.Messages {
		switch m.Role {
		case "tool":
			key := "result"
			if m.IsError {
				key = "error"
			}
			fr := &gmFuncResponse{Name: m.ToolName, Response: map[string]any{key: m.Text}}
			if !isSyntheticGoogleID(r.Messages, m.ToolCallID) {
				fr.ID = m.ToolCallID
			}
			pending = append(pending, gmPart{FunctionResponse: fr})
		case "assistant":
			flush()
			var parts []gmPart
			if m.Text != "" {
				parts = append(parts, gmPart{Text: m.Text})
			}
			for _, tc := range m.ToolCalls {
				fc := &gmFuncCall{Name: tc.Name, Args: argsOrEmpty(tc.Args)}
				if tc.Meta[MetaGoogleSyntheticID] != "1" {
					fc.ID = tc.ID
				}
				parts = append(parts, gmPart{FunctionCall: fc, ThoughtSignature: tc.Meta[MetaGoogleThoughtSignature]})
			}
			if len(parts) == 0 {
				continue
			}
			out.Contents = append(out.Contents, gmContent{Role: "model", Parts: parts})
		default:
			flush()
			out.Contents = append(out.Contents, gmContent{Role: "user", Parts: []gmPart{{Text: m.Text}}})
		}
	}
	flush()
	if len(r.Tools) > 0 {
		var decls []gmFuncDecl
		for _, t := range r.Tools {
			decls = append(decls, gmFuncDecl{Name: t.Name, Description: t.Description, ParametersJSONSchema: toolSchema(t)})
		}
		out.Tools = []gmTool{{FunctionDeclarations: decls}}
	}
	return out
}

// isSyntheticGoogleID reports whether id belongs to a tool call whose id was
// made up locally (Gemini sent none), so it must not be sent as an id.
func isSyntheticGoogleID(msgs []Message, id string) bool {
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if tc.ID == id {
				return tc.Meta[MetaGoogleSyntheticID] == "1"
			}
		}
	}
	return false
}

func googleModelPath(model string) string {
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	return "/models/" + url.PathEscape(model)
}

func setGoogleAuth(req *http.Request, p Provider) {
	if p.APIKey != "" {
		req.Header.Set("x-goog-api-key", p.APIKey)
	}
}

type gmResponse struct {
	Candidates []struct {
		Content      gmContent `json:"content"`
		FinishReason string    `json:"finishReason"`
		Index        int       `json:"index"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Error json.RawMessage `json:"error"`
}

func streamGoogle(ctx context.Context, c *http.Client, p Provider, r Request, onText func(string)) (Message, error) {
	u, err := endpoint(p, googleModelPath(r.Model)+":streamGenerateContent", url.Values{"alt": {"sse"}})
	if err != nil {
		return Message{}, err
	}
	req, err := newJSONRequest(ctx, http.MethodPost, u, buildGoogle(r))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Accept", "text/event-stream")
	setGoogleAuth(req, p)
	resp, err := do(c, KindGoogle, req)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	var (
		text   strings.Builder
		calls  []ToolCall
		finish string
		block  string
	)
	err = readSSE(resp.Body, func(ev sseEvent) error {
		if strings.TrimSpace(ev.Data) == "" {
			return nil
		}
		var gr gmResponse
		if err := json.Unmarshal([]byte(ev.Data), &gr); err != nil {
			return fmt.Errorf("bad stream chunk: %w", err)
		}
		if len(gr.Error) > 0 && string(gr.Error) != "null" {
			return &APIError{Kind: KindGoogle, Message: errorMessage([]byte(ev.Data), "error")}
		}
		if gr.PromptFeedback != nil && gr.PromptFeedback.BlockReason != "" {
			block = gr.PromptFeedback.BlockReason
		}
		for _, cand := range gr.Candidates {
			if cand.Index != 0 {
				continue
			}
			for _, part := range cand.Content.Parts {
				switch {
				case part.FunctionCall != nil:
					tc := ToolCall{ID: part.FunctionCall.ID, Name: part.FunctionCall.Name, Args: part.FunctionCall.Args}
					if tc.ID == "" {
						tc.ID = fmt.Sprintf("gemini-call-%d", len(calls))
						tc.Meta = map[string]string{MetaGoogleSyntheticID: "1"}
					}
					if part.ThoughtSignature != "" {
						if tc.Meta == nil {
							tc.Meta = map[string]string{}
						}
						tc.Meta[MetaGoogleThoughtSignature] = part.ThoughtSignature
					}
					calls = append(calls, tc)
				case part.Thought:
					// reasoning summary: not part of the answer
				case part.Text != "":
					text.WriteString(part.Text)
					onText(part.Text)
				}
			}
			if cand.FinishReason != "" {
				finish = cand.FinishReason
			}
		}
		return nil
	})
	msg := Message{Role: "assistant", Text: text.String(), ToolCalls: calls}
	if err != nil {
		return msg, err
	}
	if block != "" {
		return msg, fmt.Errorf("prompt blocked by the provider (%s)", block)
	}
	switch finish {
	case "":
		return msg, errors.New("stream ended before the model finished")
	case "STOP", "FINISH_REASON_UNSPECIFIED":
		return msg, nil
	case "MAX_TOKENS":
		return msg, errors.New("model hit the max token limit before finishing")
	default:
		return msg, fmt.Errorf("model stopped early (%s)", finish)
	}
}

func listGoogle(ctx context.Context, c *http.Client, p Provider) ([]string, error) {
	var ids []string
	token := ""
	for page := 0; page < 50; page++ {
		q := url.Values{"pageSize": {"1000"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		u, err := endpoint(p, "/models", q)
		if err != nil {
			return nil, err
		}
		req, err := newJSONRequest(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		setGoogleAuth(req, p)
		resp, err := do(c, KindGoogle, req)
		if err != nil {
			return nil, err
		}
		var out struct {
			Models []struct {
				Name    string   `json:"name"`
				Methods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		err = json.NewDecoder(limitBody(resp.Body)).Decode(&out)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("bad model list: %w", err)
		}
		for _, m := range out.Models {
			if len(m.Methods) > 0 && !contains(m.Methods, "generateContent") {
				continue // embedding, image-only, etc.
			}
			ids = append(ids, strings.TrimPrefix(m.Name, "models/"))
		}
		if out.NextPageToken == "" || out.NextPageToken == token {
			break
		}
		token = out.NextPageToken
	}
	return ids, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
