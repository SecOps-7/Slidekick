package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// scopedDeck returns the deck a request is limited to, or "" when the caller
// holds the server token (or uses stdio, which is trusted). The HTTP front
// end attaches TokenInfo for every authenticated request.
func scopedDeck(req mcp.Request) string {
	ex := req.GetExtra()
	if ex == nil || ex.TokenInfo == nil {
		return ""
	}
	id, _ := ex.TokenInfo.Extra["deck"].(string)
	return id
}

// adminOnlyTools can't run under a deck-scoped token.
var adminOnlyTools = map[string]bool{"create_deck": true}

func denied(format string, a ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, a...)}}}
}

// scopeMiddleware keeps deck-scoped tokens inside their deck: every tool call
// naming a deck_id and every per-deck resource read is checked here, so no
// individual tool can forget to.
func scopeMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		deckID := scopedDeck(req)
		if deckID == "" {
			return next(ctx, method, req)
		}
		switch p := req.GetParams().(type) {
		case *mcp.CallToolParamsRaw:
			if adminOnlyTools[p.Name] {
				return denied("%s needs the server token; this token can only edit deck %s.", p.Name, deckID), nil
			}
			var args struct {
				DeckID *string `json:"deck_id"`
			}
			json.Unmarshal(p.Arguments, &args)
			if args.DeckID != nil && *args.DeckID != deckID {
				return denied("This token can only edit deck %s.", deckID), nil
			}
		case *mcp.ReadResourceParams:
			if rest, ok := strings.CutPrefix(p.URI, "slidekick://decks/"); ok {
				if id, _, _ := strings.Cut(rest, "/"); id != deckID {
					return nil, mcp.ResourceNotFoundError(p.URI)
				}
			}
		}
		return next(ctx, method, req)
	}
}
