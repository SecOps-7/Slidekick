package presenting

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/tts"
)

// fakeDeps serves one deck and records published events on a channel.
type fakeDeps struct {
	mu   sync.Mutex
	d    *deck.Deck
	evs  chan map[string]any
	seen []map[string]any
}

func newDeps(d *deck.Deck) *fakeDeps { return &fakeDeps{d: d, evs: make(chan map[string]any, 100)} }

func (f *fakeDeps) Get(_ context.Context, id string) (*deck.Deck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != "d1" {
		return nil, errors.New("not found")
	}
	return f.d, nil
}

func (f *fakeDeps) Publish(_ string, ev any) {
	m := ev.(map[string]any)
	f.mu.Lock()
	f.seen = append(f.seen, m)
	f.mu.Unlock()
	f.evs <- m
}

func (f *fakeDeps) next(t *testing.T) map[string]any {
	t.Helper()
	select {
	case ev := <-f.evs:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
		return nil
	}
}

// fakeVoice makes tiny "audio"; browser=true answers like a browser voice.
type fakeVoice struct {
	mu      sync.Mutex
	browser bool
	said    []string
	dur     time.Duration
}

func (v *fakeVoice) Speak(_ context.Context, provider, voice, text string) (*tts.Audio, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.browser {
		return nil, tts.ErrBrowserVoice
	}
	v.said = append(v.said, text)
	return &tts.Audio{Data: []byte(text), MIME: "audio/mpeg", Duration: v.dur}, nil
}

func (v *fakeVoice) AudioURL(a *tts.Audio) string { return "/api/tts/audio/" + string(a.Data) + ".mp3" }

func el(id string, step int) deck.Element {
	e := deck.Element{ID: id, Type: "text"}
	if step > 0 {
		e.Build = &deck.Build{Step: step}
	}
	return e
}

func twoSlides() *deck.Deck {
	return &deck.Deck{Slides: []deck.Slide{
		{ID: "s1", Notes: "Welcome everyone to the review.", Regions: map[string][]deck.Element{"body": {el("a", 0)}}},
		// The stage directions in these notes are never spoken.
		{ID: "s2", Notes: "Revenue grew 12% this quarter.\n\nThe first point builds in now. Costs fell 3%.\n\nAnd finally the second point. Margins hit 41%.", Regions: map[string][]deck.Element{"body": {el("b", 1), el("c", 2)}}},
	}}
}

func TestBuildsLines(t *testing.T) {
	d := twoSlides()
	if Builds(d.Slides[0]) != 0 || Builds(d.Slides[1]) != 2 {
		t.Fatalf("builds %d %d", Builds(d.Slides[0]), Builds(d.Slides[1]))
	}
	cases := []struct {
		notes  string
		builds int
		want   []string
	}{
		{"One.\n\nTwo.", 0, []string{"One.\n\nTwo."}},
		{"One.\r\n\r\nTwo.\n\n\n\nThree.", 1, []string{"One.", "Two.\n\nThree."}},
		{"Only.", 2, []string{"Only.", "", ""}},
		{"", 1, []string{"", ""}},
	}
	for _, c := range cases {
		if got := Lines(c.notes, c.builds); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Lines(%q, %d) = %q, want %q", c.notes, c.builds, got, c.want)
		}
	}
	// Bullets with buildEach reveal one item per step from their own step.
	each := deck.Element{ID: "l", Type: "bullets", Build: &deck.Build{Step: 2}, Props: map[string]any{"buildEach": true, "items": []any{"a", "b", "c"}}}
	if n := Builds(deck.Slide{Regions: map[string][]deck.Element{"body": {el("x", 1), each}}}); n != 4 {
		t.Fatalf("buildEach builds = %d, want 4", n)
	}
}

func newManager(d *deck.Deck, v Speaker) (*Manager, *fakeDeps) {
	deps := newDeps(d)
	return &Manager{Deps: deps, Voice: v, Slack: 20 * time.Millisecond, Pause: 5 * time.Millisecond}, deps
}

func TestGoAckAndTimeout(t *testing.T) {
	v := &fakeVoice{dur: 2 * time.Second}
	m, deps := newManager(twoSlides(), v)
	ctx := context.Background()

	type out struct {
		r   *StepResult
		err error
	}
	done := make(chan out, 1)
	start := time.Now()
	go func() {
		r, err := m.Go(ctx, "d1", Step{Slide: 2, Build: 5, Say: " Hello there "}, "agent")
		done <- out{r, err}
	}()
	ev := deps.next(t)
	if ev["state"] != "step" || ev["slideId"] != "s2" || ev["index"] != 1 || ev["build"] != 2 || ev["text"] != "Hello there" ||
		ev["audio"] != "/api/tts/audio/Hello there.mp3" || ev["durationMs"] != int64(2000) || ev["actor"] != "agent" {
		t.Fatalf("event %v", ev)
	}
	seq := ev["seq"].(int)
	m.Ack("d1", seq+7) // unknown seqs are ignored
	m.Ack("d1", seq)
	o := <-done
	if o.err != nil || !o.r.Heard || o.r.Slide != 2 || o.r.Build != 2 || o.r.Builds != 2 || o.r.Voice != "" {
		t.Fatalf("result %+v %v", o.r, o.err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("an ack should end the wait at once")
	}
	m.Ack("d1", seq) // a late second ack is harmless

	// No presenting screen: the step waits for the audio's length plus slack.
	v.dur = 40 * time.Millisecond
	start = time.Now()
	r, err := m.Go(ctx, "d1", Step{Say: "Still here"}, "agent")
	if err != nil || r.Heard || r.Slide != 2 {
		t.Fatalf("timeout step %+v %v", r, err)
	}
	if el := time.Since(start); el < 40*time.Millisecond {
		t.Fatalf("returned after %v, before the audio could finish", el)
	}
	deps.next(t)

	// Moving without a line returns at once and says nothing.
	r, err = m.Go(ctx, "d1", Step{SlideID: "s1"}, "agent")
	if ev := deps.next(t); err != nil || r.Slide != 1 || ev["text"] != nil || ev["audio"] != nil {
		t.Fatalf("silent step %+v %v %v", r, err, ev)
	}
	if _, err := m.Go(ctx, "d1", Step{Slide: 9}, "agent"); err == nil {
		t.Fatal("slide 9 of 2 should fail")
	}
	if st := m.Status("d1"); st.Slide != 1 || st.Slides != 2 || st.Running {
		t.Fatalf("status %+v", st)
	}
}

func TestGoBrowserVoice(t *testing.T) {
	m, deps := newManager(twoSlides(), &fakeVoice{browser: true})
	done := make(chan *StepResult, 1)
	go func() {
		r, _ := m.Go(context.Background(), "d1", Step{Slide: 1, Say: "Hi", Voice: "Samantha"}, "agent")
		done <- r
	}()
	ev := deps.next(t)
	if ev["browserVoice"] != true || ev["voice"] != "Samantha" || ev["audio"] != nil || ev["durationMs"].(int64) <= 0 {
		t.Fatalf("event %v", ev)
	}
	m.Ack("d1", ev["seq"].(int))
	if r := <-done; r == nil || !r.Heard || r.Voice != "browser" {
		t.Fatalf("result %+v", r)
	}

	// No voice service at all behaves the same.
	m2, deps2 := newManager(twoSlides(), nil)
	go m2.Go(context.Background(), "d1", Step{Slide: 1, Say: "Hi"}, "agent")
	if ev := deps2.next(t); ev["browserVoice"] != true {
		t.Fatalf("nil voice event %v", ev)
	}
}

func TestStartChecks(t *testing.T) {
	m, _ := newManager(twoSlides(), &fakeVoice{})
	if _, err := m.Start("d1", RunOptions{From: 3, To: 2}, "agent"); err == nil {
		t.Fatal("empty range should fail")
	}
	if _, err := m.Start("d1", RunOptions{Script: []SlideScript{{Slide: 3, Lines: []string{"x"}}}}, "agent"); err == nil {
		t.Fatal("a script for slide 3 of 2 should fail")
	}
	if _, err := m.Start("d1", RunOptions{Script: []SlideScript{{Slide: 1, Lines: make([]string, 30)}}}, "agent"); err == nil {
		t.Fatal("a script with 30 lines should fail")
	}
	if m.Status("d1").Running {
		t.Fatal("a refused start left a run going")
	}
}

// ackAll plays the presenting screen: it acks every step with a line and
// returns the events it saw until the run ends.
func ackAll(t *testing.T, m *Manager, deps *fakeDeps) []map[string]any {
	t.Helper()
	var evs []map[string]any
	for {
		ev := deps.next(t)
		evs = append(evs, ev)
		switch ev["state"] {
		case "step":
			if ev["text"] != nil {
				m.Ack("d1", ev["seq"].(int))
			}
		case "ended", "stopped":
			return evs
		}
	}
}

func TestRunWithBuilds(t *testing.T) {
	v := &fakeVoice{dur: 5 * time.Second} // only acks can keep this fast
	m, deps := newManager(twoSlides(), v)
	start := time.Now()
	st, err := m.Start("d1", RunOptions{Voice: "nova"}, "user:edna")
	if err != nil || !st.Running || st.Slides != 2 || st.Actor != "user:edna" {
		t.Fatalf("start %+v %v", st, err)
	}
	if _, err := m.Start("d1", RunOptions{}, "x"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second start: %v", err)
	}
	if _, err := m.Go(context.Background(), "d1", Step{Slide: 1}, "x"); !errors.Is(err, ErrBusy) {
		t.Fatalf("manual step during a run: %v", err)
	}
	evs := ackAll(t, m, deps)
	if time.Since(start) > 2*time.Second {
		t.Fatal("acked run should finish quickly")
	}
	var got []string
	for _, ev := range evs {
		s := fmt.Sprint(ev["state"])
		if s == "step" {
			s = fmt.Sprintf("%v/%v:%v", ev["slideId"], ev["build"], ev["text"])
		}
		got = append(got, s)
	}
	want := []string{"started", "s1/0:Welcome everyone to the review.", "s2/0:Revenue grew 12% this quarter.", "s2/1:Costs fell 3%.", "s2/2:Margins hit 41%.", "ended"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %q\nwant %q", got, want)
	}
	if evs[len(evs)-1]["error"] != nil {
		t.Fatalf("clean run reported an error: %v", evs[len(evs)-1])
	}
	if st := m.Status("d1"); st.Running || st.Slide != 2 || st.Build != 2 || st.Error != "" {
		t.Fatalf("final status %+v", st)
	}
	// The deck is free again.
	if _, err := m.Go(context.Background(), "d1", Step{Slide: 1}, "x"); err != nil {
		t.Fatal(err)
	}
}

func TestStop(t *testing.T) {
	v := &fakeVoice{dur: 5 * time.Second}
	m, deps := newManager(twoSlides(), v)
	if m.Stop("d1") {
		t.Fatal("nothing was running")
	}
	if _, err := m.Start("d1", RunOptions{}, "agent"); err != nil {
		t.Fatal(err)
	}
	if ev := deps.next(t); ev["state"] != "started" {
		t.Fatalf("%v", ev)
	}
	if ev := deps.next(t); ev["state"] != "step" { // waiting on slide 1's line, unacked
		t.Fatalf("%v", ev)
	}
	if !m.Stop("d1") {
		t.Fatal("Stop should report the run")
	}
	if ev := deps.next(t); ev["state"] != "stopped" || ev["actor"] != "agent" {
		t.Fatalf("stop event %v", ev)
	}
	if m.Status("d1").Running {
		t.Fatal("still running after Stop")
	}
	// A new run may start right away; the old one says nothing more.
	if _, err := m.Start("d1", RunOptions{}, "agent"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	evs := ackAll(t, m, deps)
	for _, ev := range evs {
		if ev["state"] == "stopped" {
			t.Fatalf("stale stopped event from the first run: %v", evs)
		}
	}
	if evs[len(evs)-1]["state"] != "ended" {
		t.Fatalf("%v", evs)
	}
}

func TestRunSpeakFailure(t *testing.T) {
	m, deps := newManager(twoSlides(), failVoice{})
	if _, err := m.Start("d1", RunOptions{}, "agent"); err != nil {
		t.Fatal(err)
	}
	evs := ackAll(t, m, deps)
	last := evs[len(evs)-1]
	if last["state"] != "ended" || last["error"] == nil {
		t.Fatalf("%v", evs)
	}
	if st := m.Status("d1"); st.Running || st.Error == "" {
		t.Fatalf("status %+v", st)
	}
}

type failVoice struct{}

func (failVoice) Speak(context.Context, string, string, string) (*tts.Audio, error) {
	return nil, errors.New("provider down")
}
func (failVoice) AudioURL(*tts.Audio) string { return "" }
