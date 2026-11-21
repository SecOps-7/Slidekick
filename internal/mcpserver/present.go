package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SecOps-7/slidekick/internal/cast"
	"github.com/SecOps-7/slidekick/internal/presenting"
	"github.com/SecOps-7/slidekick/internal/service"
)

type PresentSlideIn struct {
	DeckID  string `json:"deck_id"`
	Slide   int    `json:"slide,omitempty" jsonschema:"slide number as people count them, starting at 1; omit (with no slide_id) to stay on the current slide"`
	SlideID string `json:"slide_id,omitempty" jsonschema:"slide id, instead of slide"`
	Build   int    `json:"build,omitempty" jsonschema:"build step to show: 0 = before any build, n = after n builds (the result says how many the slide has)"`
	Say     string `json:"say,omitempty" jsonschema:"what to say out loud at this step, a sentence or a short paragraph; omit to just move"`
	Voice   string `json:"voice,omitempty" jsonschema:"voice name; omit for the instance's default voice"`
}

type StartPresentationIn struct {
	DeckID    string `json:"deck_id"`
	FromSlide int    `json:"from_slide,omitempty" jsonschema:"first slide to present, starting at 1 (default 1)"`
	ToSlide   int    `json:"to_slide,omitempty" jsonschema:"last slide to present, inclusive (default: the last slide)"`
	Voice     string `json:"voice,omitempty" jsonschema:"voice name; omit for the instance's default voice"`
}

type StartPresentationOut struct {
	Status presenting.Status `json:"status"`
	Note   string            `json:"note"`
}

type StoppedOut struct {
	Stopped bool `json:"stopped" jsonschema:"false when nothing was running"`
}

type CastDeviceOut struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type CastDevicesOut struct {
	Devices []CastDeviceOut `json:"devices"`
}

type CastDeckIn struct {
	DeckID string `json:"deck_id"`
	Device string `json:"device" jsonschema:"device name (as list_cast_devices shows it, any case) or id"`
}

type CastOut struct {
	Casting *cast.Session `json:"casting" jsonschema:"where the deck is showing; null when nowhere"`
}

func action(name, desc string) *mcp.Tool { return &mcp.Tool{Name: name, Description: desc} }

func (h *handlers) castTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "list_cast_devices", Description: "List the TVs (Chromecasts) an admin has set up, to show a deck on with cast_deck.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, req *mcp.CallToolRequest, _ NoInput) (*mcp.CallToolResult, *CastDevicesOut, error) {
			ds, err := h.svc.CastDevices(ctx)
			if err != nil {
				return nil, nil, err
			}
			out := &CastDevicesOut{Devices: []CastDeviceOut{}}
			for _, d := range ds {
				out.Devices = append(out.Devices, CastDeviceOut{ID: d.ID, Name: d.Name}) // never addresses
			}
			return nil, out, nil
		})

	mcp.AddTool(s, action("cast_deck", "Show a deck on a TV (Chromecast) in presenting mode. The TV follows the presentation and plays its narration. A deck shows on one TV at a time."),
		func(ctx context.Context, req *mcp.CallToolRequest, in CastDeckIn) (*mcp.CallToolResult, *CastOut, error) {
			if err := h.svc.StartCast(ctx, in.DeckID, in.Device, actorOf(req)); err != nil {
				return nil, nil, notFound(err, in.DeckID)
			}
			return nil, &CastOut{Casting: h.svc.CastStatus(in.DeckID)}, nil
		})

	mcp.AddTool(s, action("stop_cast", "Take a deck off the TV it is showing on."),
		func(ctx context.Context, req *mcp.CallToolRequest, in DeckRef) (*mcp.CallToolResult, *StoppedOut, error) {
			err := h.svc.StopCastBy(ctx, in.DeckID, actorOf(req))
			if errors.Is(err, service.ErrNotCasting) {
				return nil, &StoppedOut{}, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return nil, &StoppedOut{Stopped: true}, nil
		})
}

func (h *handlers) presentTools(s *mcp.Server) {
	mcp.AddTool(s, action("present_slide", "Present one step out loud: show a slide (at a build step) on every screen watching the deck and speak a line in the narration voice. "+
		"The presenter view and a cast TV play the audio; other viewers follow silently. It returns after the narration has been spoken, so call it again for the next line or build step. "+
		"Use it to present step by step with your own words; start_presentation reads the speaker notes on its own instead."),
		func(ctx context.Context, req *mcp.CallToolRequest, in PresentSlideIn) (*mcp.CallToolResult, *presenting.StepResult, error) {
			res, err := h.presenter.Go(ctx, in.DeckID, presenting.Step{Slide: in.Slide, SlideID: in.SlideID, Build: in.Build, Say: in.Say, Voice: in.Voice}, actorOf(req))
			if err != nil {
				return nil, nil, notFound(err, in.DeckID)
			}
			return nil, res, nil
		})

	mcp.AddTool(s, action("start_presentation", "Present the deck by itself in the background: it shows each slide and speaks that slide's speaker notes, "+
		"stepping through builds (blank lines split a slide's notes across its build steps: the first paragraph before any build, the next after the first build, and so on). "+
		"If some slides have no notes it fails and lists them: write a spoken narration into those slides' notes with update_slide or patch_deck first, then start again. "+
		"Returns at once; follow it with presentation_status and end it with stop_presentation."),
		func(ctx context.Context, req *mcp.CallToolRequest, in StartPresentationIn) (*mcp.CallToolResult, *StartPresentationOut, error) {
			st, err := h.presenter.Start(in.DeckID, presenting.RunOptions{From: in.FromSlide, To: in.ToSlide, Voice: in.Voice}, actorOf(req))
			if err != nil {
				return nil, nil, notFound(err, in.DeckID)
			}
			return nil, &StartPresentationOut{Status: st, Note: "Presenting in the background. People need the presenter view or a cast TV open to hear it."}, nil
		})

	mcp.AddTool(s, action("stop_presentation", "Stop the deck's automatic presentation."),
		func(ctx context.Context, req *mcp.CallToolRequest, in DeckRef) (*mcp.CallToolResult, *StoppedOut, error) {
			return nil, &StoppedOut{Stopped: h.presenter.Stop(in.DeckID)}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "presentation_status", Description: "Where the deck's presentation is: running or not, current slide and build step, and why the last run stopped early.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, req *mcp.CallToolRequest, in DeckRef) (*mcp.CallToolResult, *presenting.Status, error) {
			if _, _, _, err := h.svc.Get(ctx, in.DeckID); err != nil {
				return nil, nil, notFound(err, in.DeckID)
			}
			st := h.presenter.Status(in.DeckID)
			return nil, &st, nil
		})
}
