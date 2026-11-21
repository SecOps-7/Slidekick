package presenting

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/SecOps-7/slidekick/internal/deck"
)

// fakeLLM answers every completion with reply (or err) and records prompts.
type fakeLLM struct {
	mu       sync.Mutex
	reply    string
	err      error
	pickErr  error
	calls    int
	system   string
	user     string
	provider string
	model    string
}

func (f *fakeLLM) Pick(_ context.Context, provider, model string) (string, string, error) {
	if f.pickErr != nil {
		return "", "", f.pickErr
	}
	if provider == "" {
		provider = "default"
	}
	if model == "" {
		model = "m1"
	}
	return provider, model, nil
}

func (f *fakeLLM) Complete(_ context.Context, provider, model, system, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.system, f.user, f.provider, f.model = system, user, provider, model
	return f.reply, f.err
}

func dataSlide() deck.Slide {
	return deck.Slide{ID: "s1", Layout: "title-body", Notes: "Stress that the growth came from returning customers.\n\nNext slide: costs.",
		Regions: map[string][]deck.Element{
			"title": {{ID: "h", Type: "heading", Props: map[string]any{"text": "Revenue **doubled**"}}},
			"body": {
				{ID: "st", Type: "stat", Props: map[string]any{"value": 4.1, "decimals": float64(1), "prefix": "$", "suffix": "M", "label": "Q3 revenue"}},
				{ID: "c", Type: "chart", Build: &deck.Build{Step: 1}, Props: map[string]any{"kind": "bar", "unit": "%", "caption": "Growth by quarter",
					"labels": []any{"Q1", "Q2", "Q3"}, "series": []any{map[string]any{"name": "Growth", "values": []any{4.0, 7.5, 12.0}}}}},
				{ID: "d", Type: "diagram", Build: &deck.Build{Step: 2}, Props: map[string]any{"caption": "How a customer returns",
					"source": "flowchart LR\n  A[First order] -->|30 days| B(Reminder email)\n  B --> C{Second order}"}},
				{ID: "i", Type: "image", Props: map[string]any{"src": "https://x/y.png", "alt": "A map of the three regions"}},
				{ID: "q", Type: "quote", Build: &deck.Build{Step: 2}, Props: map[string]any{"text": "They came back for the service", "attribution": "Head of sales"}},
				{ID: "k", Type: "code", Props: map[string]any{"code": "func main() {}", "language": "go"}},
			},
		}}
}

func TestContentByStep(t *testing.T) {
	c := Content(dataSlide())
	if len(c) != 3 {
		t.Fatalf("steps %d: %q", len(c), c)
	}
	want0 := []string{"Title: Revenue doubled", "Number: $4.1M (Q3 revenue)", "Image: A map of the three regions", "Code sample (go, 1 lines): func main() {}"}
	if !reflect.DeepEqual(c[0], want0) {
		t.Fatalf("step 0\n got %q\nwant %q", c[0], want0)
	}
	if len(c[1]) != 1 || c[1][0] != "Bar chart: Growth by quarter. Data: Growth: Q1 4%, Q2 7.5%, Q3 12%" {
		t.Fatalf("step 1 %q", c[1])
	}
	if len(c[2]) != 2 || !strings.Contains(c[2][0], "First order") || !strings.Contains(c[2][0], "30 days") ||
		!strings.Contains(c[2][0], "Reminder email") || !strings.Contains(c[2][0], "Second order") || c[2][1] != `Quote: "They came back for the service" (Head of sales)` {
		t.Fatalf("step 2 %q", c[2])
	}
}

func TestScriptPrompt(t *testing.T) {
	p := ScriptPrompt(ScriptInput{DeckTitle: "Q3 review", Slide: dataSlide(), Number: 2, Total: 5, Previous: "We set out three goals."})
	for _, want := range []string{"Q3 review", "We set out three goals.", "returning customers", "Title: Revenue doubled", "$4.1M", "Q3 12%",
		"Step 1 (adds)", "Step 2 (adds)", "exactly 3 strings"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	for _, rule := range []string{"Never talk about the presentation itself", "talking points", "strict JSON", `{"lines"`} {
		if !strings.Contains(scriptSystemPrompt, rule) {
			t.Errorf("system prompt lacks %q", rule)
		}
	}
}

func TestScripterWritesWithModel(t *testing.T) {
	f := &fakeLLM{reply: "```json\n{\"lines\": [\"Revenue doubled to $4.1M.\", \"Here is the chart. Growth sped up every quarter, to 12%.\", \"\"]}\n```"}
	s := &Scripter{AI: f}
	in := ScriptInput{DeckTitle: "Q3 review", Slide: dataSlide(), Number: 1, Total: 3, Provider: "p1"}
	got := s.Script(context.Background(), in)
	want := []string{"Revenue doubled to $4.1M.", "Growth sped up every quarter, to 12%.", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if f.provider != "p1" || f.model != "m1" || !strings.Contains(f.user, "returning customers") || !strings.Contains(f.user, "Revenue doubled") {
		t.Fatalf("model call %s/%s: %s", f.provider, f.model, f.user)
	}

	// Same slide, notes, title and model: from the cache.
	got[0] = "changed by the caller"
	if again := s.Script(context.Background(), in); !reflect.DeepEqual(again, want) || f.calls != 1 {
		t.Fatalf("cache: %q after %d calls", again, f.calls)
	}
	// Another model, or edited notes, writes again.
	in2 := in
	in2.Model = "m2"
	s.Script(context.Background(), in2)
	in3 := in
	in3.Slide.Notes = "Different talking points."
	s.Script(context.Background(), in3)
	if f.calls != 3 {
		t.Fatalf("calls %d, want 3", f.calls)
	}
}

func TestScripterLineCount(t *testing.T) {
	// Too few lines: the rest are silent. Too many: they join the last step.
	f := &fakeLLM{reply: `{"lines": ["Only one."]}`}
	if got := (&Scripter{AI: f}).Script(context.Background(), ScriptInput{Slide: dataSlide()}); !reflect.DeepEqual(got, []string{"Only one.", "", ""}) {
		t.Fatalf("few: %q", got)
	}
	f = &fakeLLM{reply: `Sure! {"lines": ["A.", "B.", "C.", "D."]} Hope that helps.`}
	if got := (&Scripter{AI: f}).Script(context.Background(), ScriptInput{Slide: dataSlide()}); !reflect.DeepEqual(got, []string{"A.", "B.", "C. D."}) {
		t.Fatalf("many: %q", got)
	}
}

func TestScripterFallsBack(t *testing.T) {
	notes := Fallback(dataSlide())
	if want := []string{"Stress that the growth came from returning customers.", "", ""}; !reflect.DeepEqual(notes, want) {
		t.Fatalf("fallback %q, want %q", notes, want)
	}
	cases := map[string]*fakeLLM{
		"model error":     {err: errors.New("provider down")},
		"not JSON":        {reply: "I can't do that."},
		"only meta lines": {reply: `{"lines": ["Here is the first slide.", "The chart builds in now.", "Moving on."]}`},
		"no AI set up":    {pickErr: errors.New("no AI provider is set up")},
	}
	for name, f := range cases {
		if got := (&Scripter{AI: f}).Script(context.Background(), ScriptInput{Slide: dataSlide()}); !reflect.DeepEqual(got, notes) {
			t.Errorf("%s: got %q", name, got)
		}
	}
	if got := (&Scripter{}).Script(context.Background(), ScriptInput{Slide: dataSlide()}); !reflect.DeepEqual(got, notes) {
		t.Errorf("nil AI: %q", got)
	}
	f := &fakeLLM{reply: `{"lines": ["x", "y", "z"]}`}
	if (&Scripter{AI: f}).Script(context.Background(), ScriptInput{Slide: dataSlide(), NoAI: true}); f.calls != 0 {
		t.Error("NoAI still called the model")
	}
}

func TestSummaryWithoutNotes(t *testing.T) {
	s := dataSlide()
	s.Notes = "Next slide please."
	got := Fallback(s)
	want := []string{"Revenue doubled. Q3 revenue: $4.1M.", "Growth by quarter.", "How a customer returns. As Head of sales put it, \"They came back for the service\"."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	// A chart without a caption reads its data.
	c := deck.Slide{Regions: map[string][]deck.Element{"body": {{ID: "c", Type: "chart", Props: map[string]any{"kind": "pie", "unit": "%",
		"labels": []any{"EU", "US"}, "series": []any{map[string]any{"name": "Share", "values": []any{60.0, 40.0}}, map[string]any{"name": "Ignored", "values": []any{1.0}}}}}}}}
	if got := Summary(c); !reflect.DeepEqual(got, []string{"Share: EU 60%, US 40%."}) {
		t.Fatalf("pie summary %q", got)
	}
}

func TestFit(t *testing.T) {
	if got := Fit(nil, 2); !reflect.DeepEqual(got, []string{"", ""}) {
		t.Fatalf("%q", got)
	}
	if got := Fit([]string{" a ", "", "b", "c"}, 2); !reflect.DeepEqual(got, []string{"a", "b c"}) {
		t.Fatalf("%q", got)
	}
}
