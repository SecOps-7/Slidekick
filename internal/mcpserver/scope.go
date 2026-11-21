package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SecOps-7/slidekick/internal/authz"
)

// principal is who is calling: the HTTP front end attaches it as TokenInfo
// to every request; stdio (no TokenInfo) is a trusted local agent.
func principal(req mcp.Request) authz.Principal {
	ex := req.GetExtra()
	if ex == nil {
		return authz.FromTokenInfo(nil)
	}
	return authz.FromTokenInfo(ex.TokenInfo)
}

// actorOf names the caller in deck history. Edits through MCP always read as
// an agent, so the editor highlights them and follows along.
func actorOf(req mcp.Request) string {
	p := principal(req)
	switch p.Kind {
	case authz.KindUser:
		return "agent:" + p.Username
	case authz.KindDeck:
		return "agent:deck-token"
	}
	return "agent"
}

// scopedDeck returns the only deck a deck token may see ("" otherwise).
func scopedDeck(req mcp.Request) string {
	if p := principal(req); p.Kind == authz.KindDeck {
		return p.Deck
	}
	return ""
}

// toolActions maps every tool to the permission it needs. A tool missing here
// is refused for everyone except the server token, so a new tool can't slip
// through unchecked.
var toolActions = map[string]authz.Action{
	"create_deck":     authz.Create,
	"get_deck":        authz.View,
	"list_decks":      anyone,
	"list_themes":     anyone,
	"add_slide":       authz.Edit,
	"update_slide":    authz.Edit,
	"delete_slide":    authz.Edit,
	"move_slide":      authz.Edit,
	"patch_deck":      authz.Edit,
	"set_theme":       authz.Edit,
	"lint_deck":       authz.View,
	"render_slide":    authz.View,
	"upload_asset":    authz.Upload,
	"export_deck":     authz.Export,
	"list_comments":   authz.View,
	"resolve_comment": authz.Edit,
	"get_history":     authz.View,
	"restore_version": authz.Edit,
	"set_deck_access": authz.Share,
	"focus_slide":     authz.Edit, // moves everyone's view, so only for people who may change the deck
	"get_share_url":   authz.View,
	// Presenting and casting show the deck on screens and TVs, so they need
	// the right to share it; speaking also needs the narrate permission.
	"present_slide":       authz.Share,
	"start_presentation":  authz.Share,
	"stop_presentation":   authz.Share,
	"presentation_status": authz.View,
	"list_cast_devices":   authz.Create, // editors and admins, as in the cast menu
	"cast_deck":           authz.Share,
	"stop_cast":           authz.Share,
}

// alsoNeeds lists a second permission some tools need on top of toolActions.
var alsoNeeds = map[string]authz.Action{
	"present_slide":      authz.Narrate,
	"start_presentation": authz.Narrate,
	"stop_presentation":  authz.Narrate,
}

const anyone authz.Action = "any"

func denied(format string, a ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, a...)}}}
}

// allowed checks one action, loading the deck's owner when a deck is named.
func (h *handlers) allowed(ctx context.Context, p authz.Principal, act authz.Action, deckID string) bool {
	if act == anyone {
		return p.Kind != authz.KindAnon
	}
	var d *authz.Deck
	if deckID != "" {
		owner, _ := h.svc.DeckOwner(ctx, deckID)
		d = &authz.Deck{ID: deckID, Owner: owner}
	}
	return h.policy.Can(p, act, d)
}

// scopeMiddleware enforces roles and deck ownership on every tool call and
// per-deck resource read, before any tool code runs.
func (h *handlers) scopeMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		p := principal(req)
		switch params := req.GetParams().(type) {
		case *mcp.CallToolParamsRaw:
			act, known := toolActions[params.Name]
			if !known {
				if p.Kind != authz.KindServer {
					return denied("%s isn't available to this token.", params.Name), nil
				}
				break
			}
			var args struct {
				DeckID *string `json:"deck_id"`
			}
			json.Unmarshal(params.Arguments, &args)
			id := ""
			if args.DeckID != nil {
				id = *args.DeckID
			}
			if p.Kind == authz.KindDeck && id != "" && id != p.Deck {
				return denied("This token can only edit deck %s.", p.Deck), nil
			}
			if !h.allowed(ctx, p, act, id) {
				return denied("%s", refusal(p, params.Name, act)), nil
			}
			if extra, ok := alsoNeeds[params.Name]; ok && !h.allowed(ctx, p, extra, id) {
				return denied("%s", refusal(p, params.Name, extra)), nil
			}
		case *mcp.ReadResourceParams:
			if rest, ok := strings.CutPrefix(params.URI, "slidekick://decks/"); ok {
				id, _, _ := strings.Cut(rest, "/")
				if !h.allowed(ctx, p, authz.View, id) {
					return nil, mcp.ResourceNotFoundError(params.URI)
				}
			}
		}
		return next(ctx, method, req)
	}
}

func refusal(p authz.Principal, tool string, act authz.Action) string {
	switch {
	case p.Kind == authz.KindDeck:
		return fmt.Sprintf("%s needs more than a deck token; this token can only edit deck %s.", tool, p.Deck)
	case p.Kind == authz.KindUser && p.Role == authz.RoleViewer:
		return fmt.Sprintf("%s isn't allowed: %s has the viewer role.", tool, p.Username)
	case act == authz.Edit || act == authz.Share:
		return fmt.Sprintf("%s isn't allowed: only the deck's owner or an admin can change it.", tool)
	}
	return fmt.Sprintf("%s isn't allowed for this account.", tool)
}
