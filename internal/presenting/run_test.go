package presenting

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SecOps-7/slidekick/internal/deck"
)

// fakeWriter writes "<slide id> step k" lines and records what it was asked.
type fakeWriter struct {
	mu     sync.Mutex
	ins    []ScriptInput
	called chan int // slide numbers, as they are asked for
	lines  func(in ScriptInput) []string
}

func (w *fakeWriter) Script(_ context.Context, in ScriptInput) []string {
	w.mu.Lock()
	w.ins = append(w.ins, in)
	w.mu.Unlock()
	if w.called != nil {
		w.called <- in.Number
	}
	if w.lines != nil {
		return w.lines(in)
	}
	out := make([]string, Builds(in.Slide)+1)
	for k := range out {
		out[k] = fmt.Sprintf("Line %d about %s.", k, in.Slide.ID)
	}
	return out
}

func noNotes() *deck.Deck {
	d := twoSlides()
	d.Title = "Quarterly review"
	for i := range d.Slides {
		d.Slides[i].Notes = ""
	}
	return d
}

func stepTexts(evs []map[string]any) []string {
	var got []string
	for _, ev := range evs {
		s := fmt.Sprint(ev["state"])
		if s == "step" {
			s = fmt.Sprintf("%v/%v:%v", ev["slideId"], ev["build"], ev["text"])
		}
		got = append(got, s)
	}
	return got
}

func TestStartWithoutNotesUsesWriter(t *testing.T) {
	m, deps := newManager(noNotes(), &fakeVoice{dur: 5 * time.Second})
	w := &fakeWriter{}
	m.Writer = w
	if _, err := m.Start("d1", RunOptions{AIProvider: "p", Model: "m1"}, "agent"); err != nil {
		t.Fatalf("a deck without notes should start: %v", err)
	}
	got := stepTexts(ackAll(t, m, deps))
	want := []string{"started", "s1/0:Line 0 about s1.", "s2/0:Line 0 about s2.", "s2/1:Line 1 about s2.", "s2/2:Line 2 about s2.", "ended"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %q\nwant %q", got, want)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.ins) != 2 {
		t.Fatalf("writer asked %d times", len(w.ins))
	}
	first, second := w.ins[0], w.ins[1]
	if first.Number != 1 || first.Total != 2 || first.DeckTitle != "Quarterly review" || first.Provider != "p" || first.Model != "m1" || first.Previous != "" {
		t.Fatalf("first input %+v", first)
	}
	// The next slide picks up from what was said last.
	if second.Number != 2 || second.Previous != "Line 0 about s1." {
		t.Fatalf("second input %+v", second)
	}
}

func TestStartWithoutWriterFallsBack(t *testing.T) {
	// No writer and no notes: a plain reading of the content, still spoken.
	d := &deck.Deck{Slides: []deck.Slide{{ID: "s1", Regions: map[string][]deck.Element{
		"title": {{ID: "h", Type: "heading", Props: map[string]any{"text": "Churn halved"}}},
		"body":  {{ID: "b", Type: "bullets", Build: &deck.Build{Step: 1}, Props: map[string]any{"items": []any{"Support replies in an hour", "New onboarding"}}}},
	}}}}
	m, deps := newManager(d, &fakeVoice{dur: 5 * time.Second})
	if _, err := m.Start("d1", RunOptions{}, "agent"); err != nil {
		t.Fatal(err)
	}
	got := stepTexts(ackAll(t, m, deps))
	want := []string{"started", "s1/0:Churn halved.", "s1/1:Support replies in an hour. New onboarding.", "ended"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %q\nwant %q", got, want)
	}
}

func TestCallerScript(t *testing.T) {
	m, deps := newManager(noNotes(), &fakeVoice{dur: 5 * time.Second})
	w := &fakeWriter{}
	m.Writer = w
	script := []SlideScript{{Slide: 2, Lines: []string{
		"Here is the second slide. Revenue grew 12%.",
		"Costs fell 3%.",
		"Margins hit 41%.",
		"An extra line joins the last step.",
	}}}
	if _, err := m.Start("d1", RunOptions{Script: script}, "agent"); err != nil {
		t.Fatal(err)
	}
	got := stepTexts(ackAll(t, m, deps))
	want := []string{"started", "s1/0:Line 0 about s1.", "s2/0:Revenue grew 12%.", "s2/1:Costs fell 3%.", "s2/2:Margins hit 41%. An extra line joins the last step.", "ended"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %q\nwant %q", got, want)
	}
	w.mu.Lock()
	n := len(w.ins)
	w.mu.Unlock()
	if n != 1 {
		t.Fatalf("the writer should only write the slide the script leaves out; asked %d times", n)
	}

	// A caller line that is nothing but meta-narration is written instead.
	m2, deps2 := newManager(noNotes(), &fakeVoice{dur: 5 * time.Second})
	m2.Writer = &fakeWriter{}
	if _, err := m2.Start("d1", RunOptions{To: 1, Script: []SlideScript{{Slide: 1, Lines: []string{"Let's move on to the next slide."}}}}, "agent"); err != nil {
		t.Fatal(err)
	}
	if got := stepTexts(ackAll(t, m2, deps2)); !reflect.DeepEqual(got, []string{"started", "s1/0:Line 0 about s1.", "ended"}) {
		t.Fatalf("events %q", got)
	}
}

func TestWriterOutputSanitizedAndFitted(t *testing.T) {
	m, deps := newManager(noNotes(), &fakeVoice{dur: 5 * time.Second})
	m.Writer = &fakeWriter{lines: func(in ScriptInput) []string {
		return []string{"As you can see on screen, sales moved up 12%. Sales moved up 12%."} // one line for every slide
	}}
	if _, err := m.Start("d1", RunOptions{}, "agent"); err != nil {
		t.Fatal(err)
	}
	got := stepTexts(ackAll(t, m, deps))
	want := []string{"started", "s1/0:Sales moved up 12%.", "s2/0:Sales moved up 12%.", "s2/1:<nil>", "s2/2:<nil>", "ended"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %q\nwant %q", got, want)
	}
}

func TestPrefetchWhileSpeaking(t *testing.T) {
	m, deps := newManager(noNotes(), &fakeVoice{dur: 5 * time.Second})
	w := &fakeWriter{called: make(chan int, 10)}
	m.Writer = w
	if _, err := m.Start("d1", RunOptions{}, "agent"); err != nil {
		t.Fatal(err)
	}
	if n := <-w.called; n != 1 {
		t.Fatalf("first script written for slide %d", n)
	}
	deps.next(t) // started
	ev := deps.next(t)
	if ev["slideId"] != "s1" {
		t.Fatalf("%v", ev)
	}
	// Slide 1 is still being spoken (no ack yet), and slide 2 is already being written.
	select {
	case n := <-w.called:
		if n != 2 {
			t.Fatalf("prefetched slide %d", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the next slide wasn't prepared while the current one spoke")
	}
	select {
	case ev := <-deps.evs:
		t.Fatalf("slide 2 shown before slide 1 finished: %v", ev)
	case <-time.After(50 * time.Millisecond):
	}
	m.Ack("d1", ev["seq"].(int))
	got := stepTexts(ackAll(t, m, deps))
	want := []string{"s2/0:Line 0 about s2.", "s2/1:Line 1 about s2.", "s2/2:Line 2 about s2.", "ended"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %q\nwant %q", got, want)
	}
}

func TestPrefetchedAudioIsUsed(t *testing.T) {
	v := &fakeVoice{dur: 5 * time.Second}
	m, deps := newManager(noNotes(), v)
	m.Writer = &fakeWriter{}
	if _, err := m.Start("d1", RunOptions{}, "agent"); err != nil {
		t.Fatal(err)
	}
	ackAll(t, m, deps)
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.said) != 4 { // each line synthesized once, ahead of time
		t.Fatalf("synthesized %d times: %q", len(v.said), v.said)
	}
}

func TestEditedSlideIsPreparedAgain(t *testing.T) {
	d := noNotes()
	m, deps := newManager(d, &fakeVoice{dur: 5 * time.Second})
	w := &fakeWriter{called: make(chan int, 10), lines: func(in ScriptInput) []string {
		out := make([]string, Builds(in.Slide)+1)
		out[0] = "About " + in.Slide.Layout + "."
		return out
	}}
	m.Writer = w
	d.Slides[1].Layout = "old"
	if _, err := m.Start("d1", RunOptions{}, "agent"); err != nil {
		t.Fatal(err)
	}
	<-w.called
	deps.next(t) // started
	ev := deps.next(t)
	<-w.called // slide 2 prepared from the old version
	deps.mu.Lock()
	edited := *d
	edited.Slides = append([]deck.Slide(nil), d.Slides...)
	edited.Slides[1].Layout = "new"
	deps.d = &edited
	deps.mu.Unlock()
	m.Ack("d1", ev["seq"].(int))
	got := stepTexts(ackAll(t, m, deps))
	if len(got) == 0 || !strings.Contains(strings.Join(got, "|"), "s2/0:About new.") {
		t.Fatalf("the edited slide wasn't re-prepared: %q", got)
	}
}

func TestGoSanitizesSay(t *testing.T) {
	m, deps := newManager(twoSlides(), &fakeVoice{dur: 10 * time.Millisecond})
	r, err := m.Go(context.Background(), "d1", Step{Slide: 2, Say: "Here is the second slide."}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if ev := deps.next(t); ev["text"] != nil || ev["audio"] != nil {
		t.Fatalf("meta-narration was spoken: %v", ev)
	}
	if r.Said != "" || r.Note == "" {
		t.Fatalf("result %+v", r)
	}
	done := make(chan *StepResult, 1)
	go func() {
		r, _ := m.Go(context.Background(), "d1", Step{Slide: 2, Say: "Moving on, revenue grew 12% as the next bullet appears now. Revenue grew 12%."}, "agent")
		done <- r
	}()
	ev := deps.next(t)
	if ev["text"] != "Revenue grew 12%." {
		t.Fatalf("spoken %q", ev["text"])
	}
	m.Ack("d1", ev["seq"].(int))
	if r := <-done; r.Said != "Revenue grew 12%." || r.Note != "" {
		t.Fatalf("result %+v", r)
	}
}
