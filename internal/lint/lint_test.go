package lint_test

import (
	"encoding/json"
	"testing"

	"github.com/SecOps-7/slidekick/internal/lint"
	"github.com/SecOps-7/slidekick/internal/testenv"
)

func codes(issues []lint.Issue) map[string]int {
	m := map[string]int{}
	for _, i := range issues {
		m[i.Code]++
	}
	return m
}

func TestGoldenDeckHasNoErrorsOrWarnings(t *testing.T) {
	env := testenv.New(t)
	d, err := env.Validator.Validate(testenv.Example(t, "launch.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range lint.Deck(d, env.Cat, env.Themes) {
		if i.Severity != "info" {
			t.Errorf("golden deck: %+v", i)
		}
	}
}

func TestRules(t *testing.T) {
	env := testenv.New(t)
	raw := `{"schemaVersion":1,"id":"d1","title":"T","theme":"paper","aspect":"16:9","slides":[
	 {"id":"s1","layout":"title-body","regions":{"body":[
	   {"id":"b","type":"bullets","props":{"items":["a","b","c","d","e","f","g","this bullet is far too long to sit on a slide because it keeps going and going and going well past any sensible limit"]}},
	   {"id":"i","type":"image","props":{"src":"x.png"}}
	 ]}},
	 {"id":"s2","layout":"title","transition":{"type":"morph"},"regions":{"title":[
	   {"id":"h","type":"heading","props":{"text":"Hi"},"style":{"color":"bg"}},
	   {"id":"r","type":"text","props":{"text":"raw"},"style":{"colorRaw":"#ff00ff"}}
	 ]}},
	 {"id":"s3","layout":"quote","regions":{}}
	]}`
	d, err := env.Validator.Validate([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	got := codes(lint.Deck(d, env.Cat, env.Themes))
	for _, want := range []string{"too-many-bullets", "long-bullet", "missing-alt", "missing-title", "morph-no-match", "low-contrast", "raw-color", "empty-slide"} {
		if got[want] == 0 {
			b, _ := json.MarshalIndent(got, "", " ")
			t.Errorf("expected %s, got %s", want, b)
		}
	}
}

func TestContrast(t *testing.T) {
	if r, _ := lint.Contrast("#000000", "#ffffff"); r < 20.9 || r > 21.1 {
		t.Fatalf("black on white = %v", r)
	}
	if r, _ := lint.Contrast("#777777", "#777777"); r != 1 {
		t.Fatalf("same colour = %v", r)
	}
}
