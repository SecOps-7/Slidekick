// Package mcpserver exposes the deck service over MCP: tools for reading and
// editing decks, resources describing the schema and catalogues, and prompts
// for common agent workflows.
package mcpserver

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SecOps-7/slidekick/internal/authz"
	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/export"
	"github.com/SecOps-7/slidekick/internal/lint"
	"github.com/SecOps-7/slidekick/internal/presenting"
	"github.com/SecOps-7/slidekick/internal/render"
	"github.com/SecOps-7/slidekick/internal/service"
	"github.com/SecOps-7/slidekick/internal/store"
)

//go:embed guide.md
var designGuide string

const instructions = `Slidekick builds animated slide decks from structured JSON. A human usually has the live viewer open and watches you edit.

Workflow:
1. Read slidekick://guide and slidekick://catalog/layouts once (they are short).
2. create_deck with a title, theme and (optionally) all slides at once.
3. Every write returns the new version and lint findings. Fix every "error" and most "warning" findings before you finish.
4. For precise edits use patch_deck (RFC 6902 JSON Patch). Pass base_version to avoid overwriting a human's edits; on a conflict, call get_deck and retry.
5. If a write result shows openComments, call list_comments: humans pinned requests for you. Act on them, then resolve_comment with a short reply.
6. Check your work visually: render_slide returns a screenshot of any slide (when Chromium is installed).
7. Give the user the url from get_share_url so they can watch or present. export_deck makes a standalone HTML file.
8. Work where the user can see it: when the user names a slide ("slide 4") or you move on to another slide, call focus_slide first, so their editor and viewer show that slide while you change it. Edits move the view too.
9. To present a deck with a spoken voice: have the user open the presenter view (the share url + "/present"), or cast_deck to a TV from list_cast_devices; those screens play the narration. Then either start_presentation for a full run that presents on its own (it writes each slide's narration from the slide's content, with the speaker notes as talking points; slides don't need notes; pass script to give your own words for some slides; stop_presentation ends it, presentation_status follows it), or drive it yourself with present_slide, one line per call, which returns once the line has been spoken. Speak as the presenter about the content and data on the slide, using the notes as talking points rather than a script; never narrate navigation, transitions, builds or clicks ("here is the next slide", "this point builds in"): the audience sees the slides change, and such sentences are dropped. Speaker notes you write are talking points for a human presenter, never stage directions.

Don't write HTML: compose elements (heading, text, bullets, image, code, stat, quote, chart, diagram, icon, shape, embed) inside layout regions. The sandboxed html element is a last resort for something no element can express; lint warns on it; style only with theme tokens. Upload images with upload_asset and use the returned /files/... url.`

// Options carries optional collaborators.
type Options struct {
	Renderer *render.Renderer // nil or unavailable: render_slide explains how to enable it
	Exporter *export.Exporter
	// Files makes PDF or PNG exports and stores them as downloadable files.
	Files func(ctx context.Context, d *deck.Deck, format string) (*store.Asset, error)
	// Policy decides what each caller may do (roles, deck owners).
	Policy authz.Policy
	// Presenter runs narrated presentations; nil leaves the presenting tools out.
	Presenter *presenting.Manager
}

// New builds the MCP server around a deck service.
func New(svc *service.Service, schemaFS fs.FS, version string, opt Options) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "slidekick", Title: "Slidekick", Version: version},
		&mcp.ServerOptions{Instructions: instructions})
	h := &handlers{svc: svc, render: opt.Renderer, export: opt.Exporter, files: opt.Files, policy: opt.Policy, presenter: opt.Presenter}
	s.AddReceivingMiddleware(h.scopeMiddleware)
	h.tools(s)
	resources(s, svc, schemaFS)
	prompts(s)
	return s
}

type handlers struct {
	svc    *service.Service
	render *render.Renderer
	export *export.Exporter
	files  func(ctx context.Context, d *deck.Deck, format string) (*store.Asset, error)
	policy authz.Policy
	// presenter is nil when presenting isn't set up.
	presenter *presenting.Manager
}

// ---- tool inputs and outputs ----

type CreateDeckIn struct {
	Title  string `json:"title" jsonschema:"deck title"`
	Theme  string `json:"theme,omitempty" jsonschema:"theme name from list_themes; defaults to aurora"`
	Aspect string `json:"aspect,omitempty" jsonschema:"16:9 (default) or 4:3"`
	Slides []any  `json:"slides,omitempty" jsonschema:"optional slides to start with, each matching the slide schema; ids are assigned when omitted"`
}

type SetAccessIn struct {
	DeckID  string `json:"deck_id"`
	Private bool   `json:"private" jsonschema:"true: only people with the link (which carries a key) can watch"`
	Rotate  bool   `json:"rotate,omitempty" jsonschema:"issue a new key; old links stop working"`
}

func keyParam(k string) string {
	if k == "" {
		return ""
	}
	return "&k=" + url.QueryEscape(k)
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
	URL     string `json:"url" jsonschema:"live viewer; arrow keys step through builds and slides, F for fullscreen"`
	Viewers int    `json:"viewers" jsonschema:"browsers currently watching"`
}

type NoInput struct{}

type CommentsIn struct {
	DeckID string `json:"deck_id"`
	All    bool   `json:"all,omitempty" jsonschema:"include resolved comments"`
}

type CommentsOut struct {
	Comments []store.Comment `json:"comments"`
}

type ResolveIn struct {
	DeckID    string `json:"deck_id"`
	CommentID string `json:"comment_id"`
	Reply     string `json:"reply,omitempty" jsonschema:"short note on what you changed, shown to the human"`
}

type OK struct {
	OK bool `json:"ok"`
}

type HistoryIn struct {
	DeckID string `json:"deck_id"`
	Limit  int    `json:"limit,omitempty" jsonschema:"how many versions, newest first (default 20)"`
}

type HistoryEntry struct {
	Version   int    `json:"version"`
	Actor     string `json:"actor" jsonschema:"agent or human"`
	Ops       int    `json:"ops" jsonschema:"number of patch operations in this version"`
	CreatedAt string `json:"createdAt"`
}

type HistoryOut struct {
	Versions []HistoryEntry `json:"versions"`
}

type RestoreIn struct {
	DeckID      string `json:"deck_id"`
	BaseVersion *int   `json:"base_version,omitempty"`
	Version     int    `json:"version" jsonschema:"version to bring back; it is applied as a new version"`
}

type UploadIn struct {
	Filename   string `json:"filename" jsonschema:"original file name, e.g. team.jpg"`
	MimeType   string `json:"mime_type" jsonschema:"image/png, image/jpeg, image/gif, image/webp, image/avif, image/svg+xml, video/mp4 or video/webm"`
	DataBase64 string `json:"data_base64" jsonschema:"file contents, base64-encoded (max 15 MB decoded)"`
}

type UploadOut struct {
	ID          string `json:"id"`
	URL         string `json:"url" jsonschema:"use this as an image src, background.image or video embed src"`
	AbsoluteURL string `json:"absoluteUrl"`
	MIME        string `json:"mime"`
	Size        int64  `json:"size"`
}

type ExportIn struct {
	DeckID string `json:"deck_id"`
	Format string `json:"format,omitempty" jsonschema:"html (default; one self-contained file), pdf (one page per slide) or png (zip of slide images)"`
}

type ExportOut struct {
	URL      string `json:"url" jsonschema:"download link for the standalone file"`
	Filename string `json:"filename"`
	Bytes    int    `json:"bytes"`
	Note     string `json:"note"`
}

type RenderIn struct {
	DeckID    string `json:"deck_id"`
	Slide     *int   `json:"slide,omitempty" jsonschema:"zero-based slide index; use this or slide_id"`
	SlideID   string `json:"slide_id,omitempty"`
	BuildStep *int   `json:"build_step,omitempty" jsonschema:"show the slide after this many build steps; omit for fully built"`
	Width     int    `json:"width,omitempty" jsonschema:"screenshot width in pixels, 640 to 1920 (default 1280)"`
}

type RenderOut struct {
	Slide   int          `json:"slide"`
	SlideID string       `json:"slideId"`
	Version int          `json:"version"`
	Width   int          `json:"width"`
	Height  int          `json:"height"`
	Lint    []lint.Issue `json:"lint" jsonschema:"lint findings for this slide"`
}

func (h *handlers) tools(s *mcp.Server) {
	write := func(name, desc string) *mcp.Tool {
		return &mcp.Tool{Name: name, Description: desc + " Returns the new version and lint findings."}
	}
	read := func(name, desc string) *mcp.Tool {
		return &mcp.Tool{Name: name, Description: desc, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}
	}

	mcp.AddTool(s, write("create_deck", "Create a new deck, optionally with all its slides in one call."),
		func(ctx context.Context, req *mcp.CallToolRequest, in CreateDeckIn) (*mcp.CallToolResult, *service.Result, error) {
			r, err := h.svc.CreateFor(ctx, principal(req).UserID, in.Title, in.Theme, in.Aspect, in.Slides, actorOf(req))
			return nil, r, err
		})

	mcp.AddTool(s, read("get_deck", "Read a deck's full JSON and current version."),
		func(ctx context.Context, req *mcp.CallToolRequest, in DeckRef) (*mcp.CallToolResult, *GetDeckOut, error) {
			_, raw, ver, err := h.svc.Get(ctx, in.DeckID)
			if err != nil {
				return nil, nil, notFound(err, in.DeckID)
			}
			var doc any
			_ = json.Unmarshal(raw, &doc)
			return nil, &GetDeckOut{Version: ver, Deck: doc, URL: h.svc.ShareURL(in.DeckID)}, nil
		})

	mcp.AddTool(s, read("list_decks", "List decks, most recently edited first."),
		func(ctx context.Context, req *mcp.CallToolRequest, _ NoInput) (*mcp.CallToolResult, *ListDecksOut, error) {
			rows, err := h.svc.List(ctx)
			if err != nil {
				return nil, nil, err
			}
			out := &ListDecksOut{Decks: []DeckSummary{}}
			only := scopedDeck(req)
			for _, r := range rows {
				if only != "" && r.ID != only {
					continue
				}
				out.Decks = append(out.Decks, DeckSummary{ID: r.ID, Title: r.Title, Theme: r.Theme, Version: r.Version,
					Slides: r.Slides, UpdatedAt: r.UpdatedAt.Format(time.RFC3339), URL: h.svc.ShareURL(r.ID)})
			}
			return nil, out, nil
		})

	mcp.AddTool(s, write("add_slide", "Add one slide."),
		func(ctx context.Context, req *mcp.CallToolRequest, in AddSlideIn) (*mcp.CallToolResult, *service.Result, error) {
			r, err := h.svc.AddSlide(ctx, in.DeckID, in.BaseVersion, in.Slide, in.Position, actorOf(req))
			return nil, r, notFound(err, in.DeckID)
		})

	mcp.AddTool(s, write("update_slide", "Replace some top-level fields of a slide (for example its regions or transition)."),
		func(ctx context.Context, req *mcp.CallToolRequest, in UpdateSlideIn) (*mcp.CallToolResult, *service.Result, error) {
			r, err := h.svc.UpdateSlide(ctx, in.DeckID, in.BaseVersion, in.SlideID, in.Fields, actorOf(req))
			return nil, r, notFound(err, in.DeckID)
		})

	mcp.AddTool(s, write("delete_slide", "Delete a slide."),
		func(ctx context.Context, req *mcp.CallToolRequest, in SlideRef) (*mcp.CallToolResult, *service.Result, error) {
			r, err := h.svc.DeleteSlide(ctx, in.DeckID, in.BaseVersion, in.SlideID, actorOf(req))
			return nil, r, notFound(err, in.DeckID)
		})

	mcp.AddTool(s, write("move_slide", "Move a slide to a new position."),
		func(ctx context.Context, req *mcp.CallToolRequest, in MoveSlideIn) (*mcp.CallToolResult, *service.Result, error) {
			r, err := h.svc.MoveSlide(ctx, in.DeckID, in.BaseVersion, in.SlideID, in.To, actorOf(req))
			return nil, r, notFound(err, in.DeckID)
		})

	mcp.AddTool(s, write("patch_deck", "Apply RFC 6902 JSON Patch operations to the deck atomically. Best for precise element edits, e.g. "+
		`{"op":"replace","path":"/slides/2/regions/body/0/props/text","value":"..."}. All ops succeed or none do.`),
		func(ctx context.Context, req *mcp.CallToolRequest, in PatchDeckIn) (*mcp.CallToolResult, *service.Result, error) {
			r, err := h.svc.Patch(ctx, in.DeckID, in.BaseVersion, in.Ops, actorOf(req))
			return nil, r, notFound(err, in.DeckID)
		})

	mcp.AddTool(s, write("set_theme", "Switch the deck's theme. Content is untouched; the look changes."),
		func(ctx context.Context, req *mcp.CallToolRequest, in SetThemeIn) (*mcp.CallToolResult, *service.Result, error) {
			r, err := h.svc.SetTheme(ctx, in.DeckID, in.BaseVersion, in.Theme, actorOf(req))
			return nil, r, notFound(err, in.DeckID)
		})

	mcp.AddTool(s, read("list_themes", "List themes with their descriptions and colour tokens."),
		func(ctx context.Context, req *mcp.CallToolRequest, _ NoInput) (*mcp.CallToolResult, *ListThemesOut, error) {
			return nil, &ListThemesOut{Themes: themeInfos(h.svc)}, nil
		})

	mcp.AddTool(s, read("lint_deck", "Check a deck for overflow, density, contrast, alt text and animation problems, with fix hints."),
		func(ctx context.Context, req *mcp.CallToolRequest, in DeckRef) (*mcp.CallToolResult, *LintOut, error) {
			issues, ver, err := h.svc.Lint(ctx, in.DeckID)
			if err != nil {
				return nil, nil, notFound(err, in.DeckID)
			}
			return nil, &LintOut{DeckID: in.DeckID, Version: ver, Summary: summarize(issues), Issues: issues}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "upload_asset", Description: "Upload an image or video and get a /files/... url to use in the deck. Identical files get the same url."},
		func(ctx context.Context, req *mcp.CallToolRequest, in UploadIn) (*mcp.CallToolResult, *UploadOut, error) {
			data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(in.DataBase64))
			if err != nil {
				return nil, nil, fmt.Errorf("data_base64 is not valid base64: %w", err)
			}
			a, err := h.svc.UploadAsset(ctx, in.Filename, in.MimeType, data)
			if err != nil {
				return nil, nil, err
			}
			return nil, &UploadOut{ID: a.ID, URL: a.URL(), AbsoluteURL: h.svc.BaseURL + a.URL(), MIME: a.MIME, Size: a.Size}, nil
		})

	mcp.AddTool(s, read("export_deck", "Export a deck: html (one self-contained file that plays offline with all animations), pdf or png. Returns a download link."),
		func(ctx context.Context, req *mcp.CallToolRequest, in ExportIn) (*mcp.CallToolResult, *ExportOut, error) {
			if in.Format == "pdf" || in.Format == "png" {
				if h.files == nil || h.render == nil || !h.render.Available() {
					return nil, nil, render.ErrUnavailable
				}
				d, _, _, err := h.svc.Get(ctx, in.DeckID)
				if err != nil {
					return nil, nil, notFound(err, in.DeckID)
				}
				a, err := h.files(ctx, d, in.Format)
				if err != nil {
					return nil, nil, err
				}
				return nil, &ExportOut{URL: h.svc.BaseURL + a.URL(), Filename: a.Name, Bytes: int(a.Size),
					Note: "Every slide fully built. The link is unguessable and can be shared as is."}, nil
			}
			if in.Format != "" && in.Format != "html" {
				return nil, nil, fmt.Errorf("format %q is not supported; use html, pdf or png", in.Format)
			}
			if h.export == nil {
				return nil, nil, errors.New("export is not configured on this server")
			}
			d, raw, _, err := h.svc.Get(ctx, in.DeckID)
			if err != nil {
				return nil, nil, notFound(err, in.DeckID)
			}
			out, err := h.export.HTML(ctx, raw)
			if err != nil {
				return nil, nil, err
			}
			name := export.Filename(d.Title)
			return nil, &ExportOut{URL: h.svc.BaseURL + "/api/decks/" + in.DeckID + "/export.html?download=1" + keyParam(h.svc.ViewKey(ctx, in.DeckID)), Filename: name, Bytes: len(out),
				Note: "Self-contained: runtime, theme and uploaded images are inlined. Web fonts and remote images load from the internet when online."}, nil
		})

	mcp.AddTool(s, read("render_slide", "Screenshot one slide (optionally at a build step) exactly as viewers see it, to check your work visually. Returns a PNG and that slide's lint findings."),
		func(ctx context.Context, req *mcp.CallToolRequest, in RenderIn) (*mcp.CallToolResult, *RenderOut, error) {
			if h.render == nil || !h.render.Available() {
				return nil, nil, render.ErrUnavailable
			}
			d, _, ver, err := h.svc.Get(ctx, in.DeckID)
			if err != nil {
				return nil, nil, notFound(err, in.DeckID)
			}
			idx := 0
			switch {
			case in.SlideID != "":
				if idx = d.SlideIndex(in.SlideID); idx < 0 {
					return nil, nil, fmt.Errorf("no slide with id %q", in.SlideID)
				}
			case in.Slide != nil:
				idx = *in.Slide
			}
			if idx < 0 || idx >= len(d.Slides) {
				return nil, nil, fmt.Errorf("slide %d is out of range 0..%d", idx, len(d.Slides)-1)
			}
			w := in.Width
			if w == 0 {
				w = 1280
			}
			if w < 640 || w > 1920 {
				return nil, nil, errors.New("width must be between 640 and 1920")
			}
			hgt := w * 9 / 16
			if d.Aspect == "4:3" {
				hgt = w * 3 / 4
			}
			step := -1
			if in.BuildStep != nil {
				step = *in.BuildStep
			}
			png, err := h.render.Slide(ctx, in.DeckID, idx, step, w, hgt)
			if err != nil {
				return nil, nil, err
			}
			var mine []lint.Issue
			for _, is := range lint.Deck(d, h.svc.Cat, h.svc.Themes) {
				if is.Slide == idx {
					mine = append(mine, is)
				}
			}
			if mine == nil {
				mine = []lint.Issue{}
			}
			out := &RenderOut{Slide: idx, SlideID: d.Slides[idx].ID, Version: ver, Width: w, Height: hgt, Lint: mine}
			summary, _ := json.Marshal(out)
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.ImageContent{Data: png, MIMEType: "image/png"},
				&mcp.TextContent{Text: string(summary)},
			}}, out, nil
		})

	mcp.AddTool(s, read("list_comments", "Read notes humans pinned to the deck, its slides or elements (\"Send to agent\"). Act on them, then resolve them."),
		func(ctx context.Context, req *mcp.CallToolRequest, in CommentsIn) (*mcp.CallToolResult, *CommentsOut, error) {
			cs, err := h.svc.Comments(ctx, in.DeckID, in.All)
			if err != nil {
				return nil, nil, notFound(err, in.DeckID)
			}
			return nil, &CommentsOut{Comments: cs}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "resolve_comment", Description: "Mark a human's comment as done, with a short reply describing what you changed."},
		func(ctx context.Context, req *mcp.CallToolRequest, in ResolveIn) (*mcp.CallToolResult, *OK, error) {
			if err := h.svc.ResolveComment(ctx, in.DeckID, in.CommentID, in.Reply); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return nil, nil, fmt.Errorf("no comment %q on deck %q", in.CommentID, in.DeckID)
				}
				return nil, nil, err
			}
			return nil, &OK{OK: true}, nil
		})

	mcp.AddTool(s, read("get_history", "List a deck's versions (who changed it and when), newest first."),
		func(ctx context.Context, req *mcp.CallToolRequest, in HistoryIn) (*mcp.CallToolResult, *HistoryOut, error) {
			if _, _, _, err := h.svc.Get(ctx, in.DeckID); err != nil {
				return nil, nil, notFound(err, in.DeckID)
			}
			limit := in.Limit
			if limit <= 0 || limit > 200 {
				limit = 20
			}
			vs, err := h.svc.History(ctx, in.DeckID, limit)
			if err != nil {
				return nil, nil, err
			}
			out := &HistoryOut{Versions: []HistoryEntry{}}
			for _, v := range vs {
				var ops []json.RawMessage
				json.Unmarshal(v.Ops, &ops)
				out.Versions = append(out.Versions, HistoryEntry{Version: v.Version, Actor: v.Actor, Ops: len(ops), CreatedAt: v.CreatedAt.Format(time.RFC3339)})
			}
			return nil, out, nil
		})

	mcp.AddTool(s, write("restore_version", "Bring back the deck exactly as it was at an earlier version (undo). History is kept: the restore is a new version."),
		func(ctx context.Context, req *mcp.CallToolRequest, in RestoreIn) (*mcp.CallToolResult, *service.Result, error) {
			r, err := h.svc.Restore(ctx, in.DeckID, in.BaseVersion, in.Version, actorOf(req))
			return nil, r, notFound(err, in.DeckID)
		})

	mcp.AddTool(s, write("set_deck_access", "Make a deck private (viewers need a link carrying its key) or public again. rotate=true issues a new key, so links shared so far stop working."),
		func(ctx context.Context, req *mcp.CallToolRequest, in SetAccessIn) (*mcp.CallToolResult, *service.Access, error) {
			a, err := h.svc.SetPrivate(ctx, in.DeckID, in.Private, in.Rotate)
			return nil, a, notFound(err, in.DeckID)
		})

	mcp.AddTool(s, read("get_share_url", "Get the live viewer link for a deck to give to the user."),
		func(ctx context.Context, req *mcp.CallToolRequest, in DeckRef) (*mcp.CallToolResult, *ShareOut, error) {
			if _, _, _, err := h.svc.Get(ctx, in.DeckID); err != nil {
				return nil, nil, notFound(err, in.DeckID)
			}
			return nil, &ShareOut{URL: h.svc.ShareURL(in.DeckID), Viewers: h.svc.Hub.Count(in.DeckID)}, nil
		})

	mcp.AddTool(s, write("focus_slide", "Bring a slide into view for everyone watching the deck (editor and live viewers that follow along). Call it before you work on a slide, and whenever the user mentions one (\"slide 4\"), so they see the slide you're changing. Edits also move the view on their own; this is for reading, explaining or starting work on a slide."),
		func(ctx context.Context, req *mcp.CallToolRequest, in FocusIn) (*mcp.CallToolResult, *FocusOut, error) {
			out, err := h.svc.Focus(ctx, in.DeckID, in.Slide, in.SlideID, actorOf(req))
			if err != nil {
				return nil, nil, notFound(err, in.DeckID)
			}
			return nil, &FocusOut{SlideID: out.SlideID, Slide: out.Index + 1, Watching: h.svc.Hub.Count(in.DeckID)}, nil
		})

	h.castTools(s)
	if h.presenter != nil {
		h.presentTools(s)
	}
}

type FocusIn struct {
	DeckID  string `json:"deck_id"`
	Slide   int    `json:"slide,omitempty" jsonschema:"slide number as people count them, starting at 1"`
	SlideID string `json:"slide_id,omitempty" jsonschema:"slide id, instead of slide"`
}

type FocusOut struct {
	SlideID  string `json:"slideId"`
	Slide    int    `json:"slide" jsonschema:"slide number, starting at 1"`
	Watching int    `json:"watching" jsonschema:"browsers watching the deck"`
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
	add("slidekick://catalog/elements", "elements", "Every element type with its props, plus the icon names.", "text/markdown",
		func() (string, error) {
			var b strings.Builder
			b.WriteString("# Elements\n\nEach element is `{type, props, style?, build?}`; ids are optional on create.\n\n")
			writeMap(&b, svc.Cat.Elements)
			b.WriteString("\n## Icons\n\nUse with `{\"type\":\"icon\",\"props\":{\"name\":\"rocket\"}}`:\n\n")
			b.WriteString(strings.Join(svc.Cat.Icons, ", "))
			b.WriteString("\n")
			return b.String(), nil
		})
	s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "slidekick://decks/{deck_id}/comments", Name: "deck-comments",
		Description: "Open comments humans pinned to a deck for the agent.", MIMEType: "application/json"},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			uri := req.Params.URI
			id := strings.TrimSuffix(strings.TrimPrefix(uri, "slidekick://decks/"), "/comments")
			cs, err := svc.Comments(ctx, id, false)
			if err != nil {
				return nil, mcp.ResourceNotFoundError(uri)
			}
			b, _ := json.MarshalIndent(cs, "", "  ")
			return text(uri, "application/json", string(b)), nil
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
