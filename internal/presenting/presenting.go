// Package presenting lets an AI (the built-in assistant or an agent over MCP)
// drive a deck as a live, narrated presentation.
//
// A presentation is a sequence of steps. Each step shows a slide at a build
// step and optionally speaks a line: the text is synthesized by the configured
// voice, and a "present" live event tells every browser watching the deck to
// go there. Presenting screens (the presenter view and a cast TV) play the
// audio and report back when it has finished; other viewers follow silently.
// A step returns once the line has been heard, so the caller paces itself on
// the narration. When no presenting screen answers, the step waits for the
// audio's own length (or an estimate for browser voices) instead.
package presenting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/service"
	"github.com/SecOps-7/slidekick/internal/tts"
)

// Speaker turns text into audio with a hosted URL (narration.Service).
type Speaker interface {
	Speak(ctx context.Context, providerID, voice, text string) (*tts.Audio, error)
	AudioURL(a *tts.Audio) string
}

// Deps is what the manager needs from the rest of the server.
type Deps interface {
	Get(ctx context.Context, deckID string) (*deck.Deck, error)
	Publish(deckID string, ev any)
}

// SvcDeps adapts a *service.Service to Deps.
type SvcDeps struct{ Svc *service.Service }

func (d SvcDeps) Get(ctx context.Context, id string) (*deck.Deck, error) {
	dk, _, _, err := d.Svc.Get(ctx, id)
	return dk, err
}
func (d SvcDeps) Publish(id string, ev any) { d.Svc.Publish(id, ev) }

// Manager runs presentations, at most one automatic run per deck.
type Manager struct {
	Deps  Deps
	Voice Speaker // nil: every line uses the presenting screen's browser voice
	// Slack is added to the audio length before a step stops waiting for the
	// presenting screen; Pause is the gap after an unnarrated slide or build.
	Slack, Pause time.Duration

	mu    sync.Mutex
	shows map[string]*show
}

type show struct {
	seq     int
	waiting map[int]chan struct{}
	cancel  context.CancelFunc // set while an automatic run is going
	runID   int
	status  Status
}

// Status describes a deck's presentation.
type Status struct {
	Running   bool      `json:"running" jsonschema:"an automatic run is in progress"`
	Slide     int       `json:"slide" jsonschema:"current slide number, starting at 1 (0 before the first step)"`
	Build     int       `json:"build" jsonschema:"current build step on that slide"`
	Slides    int       `json:"slides" jsonschema:"slides in the deck"`
	Actor     string    `json:"actor,omitempty"`
	StartedAt time.Time `json:"startedAt,omitzero"`
	Error     string    `json:"error,omitempty" jsonschema:"why the last automatic run stopped early"`
}

// Step is one move of the presentation.
type Step struct {
	Slide    int    // 1-based; 0 with SlideID empty means the current slide
	SlideID  string // wins over Slide
	Build    int    // build step to show (0 = before any build)
	Say      string // narration; empty = just move
	Provider string // voice provider id; "" = the instance default
	Voice    string
}

// StepResult reports what happened.
type StepResult struct {
	Slide   int    `json:"slide" jsonschema:"slide number shown, starting at 1"`
	SlideID string `json:"slideId"`
	Build   int    `json:"build"`
	Builds  int    `json:"builds" jsonschema:"build steps this slide has"`
	SpokeMs int64  `json:"spokeMs" jsonschema:"how long the narration took"`
	Heard   bool   `json:"heard" jsonschema:"a presenting screen played the narration and confirmed it finished"`
	Voice   string `json:"voice,omitempty" jsonschema:"'browser' when the presenting screen's own voice spoke it"`
}

// ErrBusy means an automatic run owns the deck.
var ErrBusy = errors.New("an automatic presentation is running on this deck; stop it first (stop_presentation)")

func (m *Manager) get(deckID string) *show {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shows == nil {
		m.shows = map[string]*show{}
	}
	sh := m.shows[deckID]
	if sh == nil {
		sh = &show{waiting: map[int]chan struct{}{}}
		m.shows[deckID] = sh
	}
	return sh
}

// Builds is the number of build steps on a slide (its highest step).
func Builds(s deck.Slide) int {
	n := 0
	for _, els := range s.Regions {
		for _, e := range els {
			if e.Build != nil && e.Build.Step > n {
				n = e.Build.Step
			}
		}
	}
	return n
}

// Go performs one step by hand. It fails while an automatic run owns the deck.
func (m *Manager) Go(ctx context.Context, deckID string, st Step, actor string) (*StepResult, error) {
	sh := m.get(deckID)
	m.mu.Lock()
	busy := sh.cancel != nil
	m.mu.Unlock()
	if busy {
		return nil, ErrBusy
	}
	return m.step(ctx, deckID, st, actor)
}

func (m *Manager) step(ctx context.Context, deckID string, st Step, actor string) (*StepResult, error) {
	d, err := m.Deps.Get(ctx, deckID)
	if err != nil {
		return nil, err
	}
	sh := m.get(deckID)
	idx := -1
	switch {
	case st.SlideID != "":
		idx = d.SlideIndex(st.SlideID)
	case st.Slide >= 1 && st.Slide <= len(d.Slides):
		idx = st.Slide - 1
	case st.Slide == 0:
		m.mu.Lock()
		idx = max(sh.status.Slide-1, 0)
		m.mu.Unlock()
	}
	if idx < 0 || idx >= len(d.Slides) {
		return nil, fmt.Errorf("no such slide (the deck has %d slides; pass slide 1 to %d)", len(d.Slides), len(d.Slides))
	}
	slide := d.Slides[idx]
	builds := Builds(slide)
	build := min(max(st.Build, 0), builds)

	res := &StepResult{Slide: idx + 1, SlideID: slide.ID, Build: build, Builds: builds}
	ev := map[string]any{"type": "present", "state": "step", "slideId": slide.ID, "index": idx, "build": build, "actor": actor}
	var wait time.Duration
	say := strings.TrimSpace(st.Say)
	if say != "" {
		ev["text"] = say
		var a *tts.Audio
		err := tts.ErrBrowserVoice
		if m.Voice != nil {
			a, err = m.Voice.Speak(ctx, st.Provider, st.Voice, say)
		}
		switch {
		case err == nil:
			ev["audio"] = m.Voice.AudioURL(a)
			wait = a.Duration
			if wait <= 0 {
				wait = estimate(say)
			}
		case errors.Is(err, tts.ErrBrowserVoice):
			ev["browserVoice"] = true
			if st.Voice != "" {
				ev["voice"] = st.Voice
			}
			res.Voice = "browser"
			wait = estimate(say)
		default:
			return nil, fmt.Errorf("couldn't make the narration audio: %w", err)
		}
		ev["durationMs"] = wait.Milliseconds()
	}

	// A run stopped while the line was being synthesized must not show one
	// more step after its "stopped" event.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	sh.seq++
	seq := sh.seq
	done := make(chan struct{})
	sh.waiting[seq] = done
	sh.status.Slide, sh.status.Build, sh.status.Slides = idx+1, build, len(d.Slides)
	if actor != "" {
		sh.status.Actor = actor
	}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(sh.waiting, seq)
		m.mu.Unlock()
	}()
	ev["seq"] = seq
	m.Deps.Publish(deckID, ev)

	if say == "" {
		return res, nil
	}
	start := time.Now()
	slack := m.Slack
	if slack == 0 {
		slack = 2 * time.Second
	}
	// The presenting screen may start late (loading audio) or play a little
	// slower; give it the audio's length plus some slack before moving on.
	timer := time.NewTimer(wait + wait/4 + slack)
	defer timer.Stop()
	select {
	case <-done:
		res.Heard = true
	case <-timer.C:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	res.SpokeMs = time.Since(start).Milliseconds()
	return res, nil
}

// Ack is called by a presenting screen when a step's narration finished.
// Unknown or late sequence numbers are ignored.
func (m *Manager) Ack(deckID string, seq int) {
	sh := m.get(deckID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := sh.waiting[seq]; ok {
		close(ch)
		delete(sh.waiting, seq)
	}
}

// Status reports a deck's presentation.
func (m *Manager) Status(deckID string) Status {
	sh := m.get(deckID)
	m.mu.Lock()
	defer m.mu.Unlock()
	return sh.status
}

// MissingNotes lists the slide numbers (from 1) with no speaker notes.
func MissingNotes(d *deck.Deck) []int {
	var out []int
	for i, s := range d.Slides {
		if strings.TrimSpace(s.Notes) == "" {
			out = append(out, i+1)
		}
	}
	return out
}

// Lines splits a slide's notes into what to say at each build step:
// paragraph i (blank-line separated) is spoken at build step i, and any
// paragraphs beyond the last build are spoken there too.
func Lines(notes string, builds int) []string {
	out := make([]string, builds+1)
	k := 0
	for _, p := range strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n\n") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if out[k] != "" {
			out[k] += "\n\n"
		}
		out[k] += p
		k = min(k+1, builds)
	}
	return out
}

// RunOptions configures an automatic run.
type RunOptions struct {
	From     int // first slide, from 1 (0 = 1)
	To       int // last slide, inclusive (0 = the end)
	Provider string
	Voice    string
}

// ErrMissingNotes means slides have nothing to say; write their notes first.
type ErrMissingNotes struct{ Slides []int }

func (e *ErrMissingNotes) Error() string {
	nums := make([]string, len(e.Slides))
	for i, n := range e.Slides {
		nums[i] = fmt.Sprint(n)
	}
	what := "slides " + strings.Join(nums, ", ") + " have"
	if len(nums) == 1 {
		what = "slide " + nums[0] + " has"
	}
	return what + " no speaker notes; write a short spoken narration into each one's notes (blank lines split it across the slide's build steps), then start again"
}

// Start begins an automatic run through the deck in the background, speaking
// each slide's notes. It returns once the run has started.
func (m *Manager) Start(deckID string, o RunOptions, actor string) (Status, error) {
	d, err := m.Deps.Get(context.Background(), deckID)
	if err != nil {
		return Status{}, err
	}
	from, to := max(o.From, 1), o.To
	if to == 0 || to > len(d.Slides) {
		to = len(d.Slides)
	}
	if from > to {
		return Status{}, fmt.Errorf("nothing to present: slides %d to %d (the deck has %d)", from, to, len(d.Slides))
	}
	var missing []int
	for _, n := range MissingNotes(d) {
		if n >= from && n <= to {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return Status{}, &ErrMissingNotes{Slides: missing}
	}

	sh := m.get(deckID)
	m.mu.Lock()
	if sh.cancel != nil {
		m.mu.Unlock()
		return Status{}, ErrBusy
	}
	ctx, cancel := context.WithCancel(context.Background())
	sh.cancel = cancel
	sh.runID++
	run := sh.runID
	sh.status = Status{Running: true, Slides: len(d.Slides), Actor: actor, StartedAt: time.Now()}
	st := sh.status
	m.mu.Unlock()

	m.Deps.Publish(deckID, map[string]any{"type": "present", "state": "started", "actor": actor})
	go m.run(ctx, deckID, run, from, to, o, actor)
	return st, nil
}

func (m *Manager) run(ctx context.Context, deckID string, run, from, to int, o RunOptions, actor string) {
	var failure error
	pause := m.Pause
	if pause == 0 {
		pause = 1200 * time.Millisecond
	}
	defer func() {
		sh := m.get(deckID)
		m.mu.Lock()
		if sh.runID == run {
			sh.cancel = nil
			sh.status.Running = false
			if failure != nil && !errors.Is(failure, context.Canceled) {
				sh.status.Error = failure.Error()
			}
		}
		m.mu.Unlock()
		if ctx.Err() != nil {
			return // Stop announced it already
		}
		ev := map[string]any{"type": "present", "state": "ended", "actor": actor}
		if failure != nil {
			ev["error"] = failure.Error()
		}
		m.Deps.Publish(deckID, ev)
	}()
	for n := from; n <= to; n++ {
		// Re-read each slide: the deck may be edited while it is presented.
		d, err := m.Deps.Get(ctx, deckID)
		if err != nil {
			failure = err
			return
		}
		if n > len(d.Slides) {
			return
		}
		s := d.Slides[n-1]
		for b, line := range Lines(s.Notes, Builds(s)) {
			if _, err := m.step(ctx, deckID, Step{SlideID: s.ID, Build: b, Say: line, Provider: o.Provider, Voice: o.Voice}, actor); err != nil {
				failure = err
				return
			}
			if line == "" {
				select {
				case <-time.After(pause):
				case <-ctx.Done():
					failure = ctx.Err()
					return
				}
			}
		}
	}
}

// Stop ends an automatic run. It reports whether one was running.
func (m *Manager) Stop(deckID string) bool {
	sh := m.get(deckID)
	m.mu.Lock()
	cancel, actor := sh.cancel, sh.status.Actor
	// Release the deck right away, so a new run or a manual step can follow
	// without waiting for the old run's goroutine to notice.
	sh.cancel = nil
	sh.status.Running = false
	m.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	m.Deps.Publish(deckID, map[string]any{"type": "present", "state": "stopped", "actor": actor})
	return true
}

// estimate is how long a browser voice takes to read text (~160 words a minute).
func estimate(text string) time.Duration {
	words := len(strings.Fields(text))
	return time.Duration(words)*375*time.Millisecond + 500*time.Millisecond
}
