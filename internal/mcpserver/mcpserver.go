// Package mcpserver exposes the deck service over MCP: tools for reading and
// editing decks, resources describing the schema and catalogues, and prompts
// for common agent workflows.
package mcpserver

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/lint"
	"github.com/SecOps-7/slidekick/internal/service"
)

//go:embed guide.md
var designGuide string

const instructions = `Slidekick builds animated slide decks from structured JSON. A human usually has the live viewer open and watches you edit.

Workflow:
1. Read slidekick://guide and slidekick://catalog/layouts once (they are short).
2. create_deck with a title, theme and (optionally) all slides at once.
3. Every write returns the new version and lint findings. Fix every "error" and most "warning" findings before you finish.
4. For precise edits use patch_deck (RFC 6902 JSON Patch). Pass base_version to avoid overwriting a human's edits; on a conflict, call get_deck and retry.
5. Give the user the url from get_share_url so they can watch or present.

Never write HTML. Compose elements (heading, text, bullets, image, code, stat, quote) inside layout regions; style only with theme tokens.`

// New builds the MCP server around a deck service.
func New(svc *service.Service, schemaFS fs.FS, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "slidekick", Title: "Slidekick", Version: version},
		&mcp.ServerOptions{Instructions: instructions})
	h := &handlers{svc: svc}
	h.tools(s)
	resources(s, svc, schemaFS)
	prompts(s)
	return s
}

type handlers struct{ svc *service.Service }

const actor = "agent"

// ---- tool inputs and outputs ----

type CreateDeckIn struct {
	Title  string `json:"title" jsonschema:"deck title"`
	Theme  string `json:"theme,omitempty" jsonschema:"theme name from list_themes; defaults to aurora"`
	Aspect string `json:"aspect,omitempty" jsonschema:"16:9 (default) or 4:3"`
	Slides []any  `json:"slides,omitempty" jsonschema:"optional slides to start with, each matching the slide schema; ids are assigned when omitted"`
}

type DeckRef struct {
	DeckID string `json:"deck_id" jsonschema:"deck id"`
}

type GetDeckOut struct {
	Version int    `json:"version"`
	Deck    any    `json:"deck" jsonschema:"the full deck document"`
	URL     string `json:"url"`
}

type DeckSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Theme     string `json:"theme"`
	Version   int    `json:"version"`
	Slides    int    `json:"slides"`
	UpdatedAt string `json:"updatedAt"`
	URL       string `json:"url"`
}

type ListDecksOut struct {
	Decks []DeckSummary `json:"decks"`
}

type AddSlideIn struct {
	DeckID      string         `json:"deck_id"`
	BaseVersion *int           `json:"base_version,omitempty" jsonschema:"version you last read; the write fails if the deck changed since"`
	Slide       map[string]any `json:"slide" jsonschema:"the slide: {layout, regions:{region:[elements]}, transition?, notes?, background?}; id optional"`
	Position    *int           `json:"position,omitempty" jsonschema:"zero-based insert position; appends when omitted"`
}

type UpdateSlideIn struct {
	DeckID      string         `json:"deck_id"`
	BaseVersion *int           `json:"base_version,omitempty"`
	SlideID     string         `json:"slide_id"`
	Fields      map[string]any `json:"fields" jsonschema:"slide fields to replace: layout, regions, transition, notes, background; null removes an optional field"`
}

type SlideRef struct {
	DeckID      string `json:"deck_id"`
	BaseVersion *int   `json:"base_version,omitempty"`
	SlideID     string `json:"slide_id"`
}

type MoveSlideIn struct {
	DeckID      string `json:"deck_id"`
	BaseVersion *int   `json:"base_version,omitempty"`
	SlideID     string `json:"slide_id"`
	To          int    `json:"to" jsonschema:"zero-based target position"`
}

type PatchDeckIn struct {
	DeckID      string    `json:"deck_id"`
	BaseVersion *int      `json:"base_version,omitempty"`
	Ops         []deck.Op `json:"ops" jsonschema:"RFC 6902 operations applied atomically, in order"`
}

type SetThemeIn struct {
	DeckID      string `json:"deck_id"`
	BaseVersion *int   `json:"base_version,omitempty"`
	Theme       string `json:"theme"`
}

type ThemeInfo struct {
	Name        string            `json:"name"`
	Label       string            `json:"label"`
	Description string            `json:"description"`
	Dark        bool              `json:"dark"`
	Colors      map[string]string `json:"colors"`
}

type ListThemesOut struct {
	Themes []ThemeInfo `json:"themes"`
}

type LintOut struct {
	DeckID  string       `json:"deckId"`
	Version int          `json:"version"`
	Summary string       `json:"summary"`
	Issues  []lint.Issue `json:"issues"`
}

type ShareOut struct {
	URL     string `json:"url" jsonschema:"live viewer; press P to present, arrow keys to step through builds"`
	Viewers int    `json:"viewers" jsonschema:"browsers currently watching"`
}

type NoInput struct{}

func (h *handlers) tools(s *mcp.Server) {
	write := func(name, desc string) *mcp.Tool {
		return &mcp.Tool{Name: name, Description: desc + " Returns the new version and lint findings."}
	}
	read := func(name, desc string) *mcp.Tool {
		return &mcp.Tool{Name: name, Description: desc, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}
	}

	mcp.AddTool(s, write("create_deck", "Create a new deck, optionally with all its slides in one call."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in CreateDeckIn) (*mcp.CallToolResult, *service.Result, error) {
			r, err := h.svc.Create(ctx, in.Title, in.Theme, in.Aspect, in.Slides, actor)
			return nil, r, err
		})

	mcp.AddTool(s, read("get_deck", "Read a deck's full JSON and current version."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in DeckRef) (*mcp.CallToolResult, *GetDeckOut, error) {
			_, raw, ver, err := h.svc.Get(ctx, in.DeckID)
			if err != nil {
				return nil, nil, notFound(err, in.DeckID)
			}
			var doc any
			_ = json.Unmarshal(raw, &doc)
			return nil, &GetDeckOut{Version: ver, Deck: doc, URL: h.svc.ShareURL(in.DeckID)}, nil
		})

	mcp.AddTool(s, read("list_decks", "List decks, most recently edited first."),
		func(ctx context.Context, _ *mcp.CallToolRequest, _ NoInput) (*mcp.CallToolResult, *ListDecksOut, error) {
			rows, err := h.svc.List(ctx)
			if err != nil {
				return nil, nil, err
			}
			out := &ListDecksOut{Decks: []DeckSummary{}}
			for _, r := range rows {
				out.Decks = append(out.Decks, DeckSummary{ID: r.ID, Title: r.Title, Theme: r.Theme, Version: r.Version,
					Slides: r.Slides, UpdatedAt: r.UpdatedAt.Format(time.RFC3339), URL: h.svc.ShareURL(r.ID)})
			}
			return nil, out, nil
		})

	mcp.AddTool(s, write("add_slide", "Add one slide."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in AddSlideIn) (*mcp.CallToolResult, *service.Result, error) {
			r, err := h.svc.AddSlide(ctx, in.DeckID, in.BaseVersion, in.Slide, in.Position, actor)
			return nil, r, notFound(err, in.DeckID)
		})

	mcp.AddTool(s, write("update_slide", "Replace some top-level fields of a slide (for example its regions or transition)."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in UpdateSlideIn) (*mcp.CallToolResult, *service.Result, error) {
			r, err := h.svc.UpdateSlide(ctx, in.DeckID, in.BaseVersion, in.SlideID, in.Fields, actor)
			return nil, r, notFound(err, in.DeckID)
		})

	mcp.AddTool(s, write("delete_slide", "Delete a slide."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in SlideRef) (*mcp.CallToolResult, *service.Result, error) {
			r, err := h.svc.DeleteSlide(ctx, in.DeckID, in.BaseVersion, in.SlideID, actor)
			return nil, r, notFound(err, in.DeckID)
		})

	mcp.AddTool(s, write("move_slide", "Move a slide to a new position."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in MoveSlideIn) (*mcp.CallToolResult, *service.Result, error) {
			r, err := h.svc.MoveSlide(ctx, in.DeckID, in.BaseVersion, in.SlideID, in.To, actor)
			return nil, r, notFound(err, in.DeckID)
		})

	mcp.AddTool(s, write("patch_deck", "Apply RFC 6902 JSON Patch operations to the deck atomically. Best for precise element edits, e.g. "+
		`{"op":"replace","path":"/slides/2/regions/body/0/props/text","value":"..."}. All ops succeed or none do.`),
		func(ctx context.Context, _ *mcp.CallToolRequest, in PatchDeckIn) (*mcp.CallToolResult, *service.Result, error) {
			r, err := h.svc.Patch(ctx, in.DeckID, in.BaseVersion, in.Ops, actor)
			return nil, r, notFound(err, in.DeckID)
		})

	mcp.AddTool(s, write("set_theme", "Switch the deck's theme. Content is untouched; the look changes."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in SetThemeIn) (*mcp.CallToolResult, *service.Result, error) {
			r, err := h.svc.SetTheme(ctx, in.DeckID, in.BaseVersion, in.Theme, actor)
			return nil, r, notFound(err, in.DeckID)
		})

	mcp.AddTool(s, read("list_themes", "List themes with their descriptions and colour tokens."),
		func(ctx context.Context, _ *mcp.CallToolRequest, _ NoInput) (*mcp.CallToolResult, *ListThemesOut, error) {
			return nil, &ListThemesOut{Themes: themeInfos(h.svc)}, nil
		})

	mcp.AddTool(s, read("lint_deck", "Check a deck for overflow, density, contrast, alt text and animation problems, with fix hints."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in DeckRef) (*mcp.CallToolResult, *LintOut, error) {
			issues, ver, err := h.svc.Lint(ctx, in.DeckID)
			if err != nil {
				return nil, nil, notFound(err, in.DeckID)
			}
			return nil, &LintOut{DeckID: in.DeckID, Version: ver, Summary: summarize(issues), Issues: issues}, nil
		})

	mcp.AddTool(s, read("get_share_url", "Get the live viewer link for a deck to give to the user."),
		func(ctx context.Context, _ *mcp.CallToolRequest, in DeckRef) (*mcp.CallToolResult, *ShareOut, error) {
			if _, _, _, err := h.svc.Get(ctx, in.DeckID); err != nil {
				return nil, nil, notFound(err, in.DeckID)
			}
			return nil, &ShareOut{URL: h.svc.ShareURL(in.DeckID), Viewers: h.svc.Hub.Count(in.DeckID)}, nil
		})
}

func notFound(err error, id string) error {
	if err == service.ErrNotFound {
		return fmt.Errorf("no deck with id %q; call list_decks to see deck ids", id)
	}
	return err
}

func summarize(issues []lint.Issue) string {
	n := map[string]int{}
	for _, i := range issues {
		n[i.Severity]++
	}
	if len(issues) == 0 {
		return "No problems found."
	}
	return fmt.Sprintf("%d errors, %d warnings, %d info", n["error"], n["warning"], n["info"])
}

func themeInfos(svc *service.Service) []ThemeInfo {
	var out []ThemeInfo
	for _, name := range svc.Themes.Names() {
		t := svc.Themes.Themes[name]
		out = append(out, ThemeInfo{Name: t.Name, Label: t.Label, Description: t.Description, Dark: t.Dark, Colors: t.Tokens.Color})
	}
	return out
}

// ---- resources ----

func resources(s *mcp.Server, svc *service.Service, schemaFS fs.FS) {
	text := func(uri, mime, body string) *mcp.ReadResourceResult {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: mime, Text: body}}}
	}
	add := func(uri, name, desc, mime string, body func() (string, error)) {
		s.AddResource(&mcp.Resource{URI: uri, Name: name, Description: desc, MIMEType: mime},
			func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				b, err := body()
				if err != nil {
					return nil, err
				}
				return text(uri, mime, b), nil
			})
	}
	add("slidekick://schema/deck", "deck-schema", "The deck JSON Schema: the contract every deck must satisfy.", "application/schema+json",
		func() (string, error) {
			b, err := fs.ReadFile(schemaFS, "schema/deck.schema.json")
			return string(b), err
		})
	add("slidekick://catalog/layouts", "layouts", "Layout presets with their region names and what each is for.", "text/markdown",
		func() (string, error) {
			var b strings.Builder
			b.WriteString("# Layouts\n\nPut elements in the regions listed for the slide's layout. Other region names are rejected.\n\n")
			names := make([]string, 0, len(svc.Cat.Layouts))
			for n := range svc.Cat.Layouts {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				fmt.Fprintf(&b, "- **%s**: %s Regions: `%s`.\n", n, svc.Cat.Layouts[n].Description, strings.Join(svc.Cat.LayoutRegions(n), "`, `"))
			}
			return b.String(), nil
		})
	add("slidekick://catalog/animations", "animations", "Slide transitions and element build presets.", "text/markdown",
		func() (string, error) {
			var b strings.Builder
			b.WriteString("# Animation presets\n\nSet `transition: {type, duration?}` on a slide (duration: fast, base or slow) and `build: {step, preset?}` on an element.\n\n## Transitions\n\n")
			writeMap(&b, svc.Cat.Transitions)
			b.WriteString("\n## Build presets\n\n")
			writeMap(&b, svc.Cat.Builds)
			b.WriteString("\nStat elements always count up when they appear. Bullets with `buildEach: true` reveal one item per step.\n")
			return b.String(), nil
		})
	add("slidekick://catalog/themes", "themes", "Themes and their colour tokens.", "application/json",
		func() (string, error) {
			b, err := json.MarshalIndent(themeInfos(svc), "", "  ")
			return string(b), err
		})
	add("slidekick://guide", "design-guide", "How to make good slides with Slidekick. Read before writing a deck.", "text/markdown",
		func() (string, error) { return designGuide, nil })
}

func writeMap(b *strings.Builder, m map[string]string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "- `%s`: %s\n", k, m[k])
	}
}

// ---- prompts ----

func prompts(s *mcp.Server) {
	user := func(text string) *mcp.GetPromptResult {
		return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}}}
	}
	s.AddPrompt(&mcp.Prompt{Name: "outline_to_deck", Title: "Outline to deck",
		Description: "Turn an outline or notes into a finished, animated deck.",
		Arguments: []*mcp.PromptArgument{
			{Name: "outline", Description: "the outline, notes or brief", Required: true},
			{Name: "theme", Description: "theme name (optional)"},
		}},
		func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			a := req.Params.Arguments
			theme := a["theme"]
			if theme == "" {
				theme = "whichever theme suits the content best (see list_themes)"
			}
			return user(fmt.Sprintf(`Build a Slidekick deck from the outline below.

1. Read slidekick://guide and slidekick://catalog/layouts.
2. Plan one idea per slide. Vary layouts; open with a title slide; use big-number or grid-3 where data or pillars appear.
3. Call create_deck once with every slide, using theme %s. Use builds so points appear as the speaker talks, and morph between slides that share elements.
4. Fix every lint error and warning in the result with patch_deck or update_slide.
5. Reply with the share url.

Outline:
%s`, theme, a["outline"])), nil
		})
	s.AddPrompt(&mcp.Prompt{Name: "polish_deck", Title: "Polish deck",
		Description: "Lint a deck and fix what it finds, tighten wording and improve motion.",
		Arguments:   []*mcp.PromptArgument{{Name: "deck_id", Description: "deck to polish", Required: true}}},
		func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return user(fmt.Sprintf(`Polish Slidekick deck %s.

1. get_deck and lint_deck.
2. Fix every error, then warnings: cut words, split dense slides, add alt text, fix contrast.
3. Make the deck move: build steps for key points, morph where consecutive slides share elements, count-up stats for headline numbers.
4. Pass base_version on every write. Repeat lint_deck until there are no errors or warnings.
5. Summarise what you changed.`, req.Params.Arguments["deck_id"])), nil
		})
	s.AddPrompt(&mcp.Prompt{Name: "restyle_deck", Title: "Restyle deck",
		Description: "Change a deck's look and feel without changing its message.",
		Arguments: []*mcp.PromptArgument{
			{Name: "deck_id", Description: "deck to restyle", Required: true},
			{Name: "direction", Description: "the feel you want, e.g. 'calm and editorial' or 'bold keynote'", Required: true},
		}},
		func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			a := req.Params.Arguments
			return user(fmt.Sprintf(`Restyle Slidekick deck %s to feel: %s.

Pick the best theme with list_themes and set_theme, then adjust layouts, transitions, build presets and emphasis (style tokens only) to match. Keep the wording. Finish with lint_deck clean.`, a["deck_id"], a["direction"])), nil
		})
}
