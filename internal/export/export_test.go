package export_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SecOps-7/slidekick/internal/export"
	"github.com/SecOps-7/slidekick/internal/testenv"
)

func exporter(t *testing.T) *export.Exporter {
	env := testenv.New(t)
	web := fstest.MapFS{
		"standalone/slidekick.js":      {Data: []byte("/*full*/ var s = '</script>';")},
		"standalone/slidekick-lite.js": {Data: []byte("/*lite*/ var s = '</SCRIPT>';")},
		"standalone/slidekick.css":     {Data: []byte(".sk-stage{}")},
	}
	return &export.Exporter{Web: web, Cat: env.Cat, Themes: env.Themes,
		Assets: func(_ context.Context, name string) (string, []byte, error) {
			if name == "f_00000000000000000001.png" {
				return "image/png", []byte{0x89, 'P', 'N', 'G'}, nil
			}
			return "", nil, errors.New("missing")
		}}
}

const deckJSON = `{"schemaVersion":1,"id":"d1","title":"Q3 <Review> & plan","theme":"paper","aspect":"16:9","slides":[
 {"id":"s1","layout":"hero-image","regions":{"image":[{"id":"i","type":"image","props":{"src":"/files/f_00000000000000000001.png","alt":"x"}}],
  "title":[{"id":"h","type":"heading","props":{"text":"</script><b>hi"}}]}}]}`

func TestStandaloneHTML(t *testing.T) {
	out, err := exporter(t).HTML(context.Background(), []byte(deckJSON))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"<title>Q3 &lt;Review&gt; &amp; plan</title>",
		`<style data-sk-theme="paper">`,
		`id="sk-data"`,
		"data:image/png;base64,iVBORw==",
		"/*lite*/",
		`<\/SCRIPT>`,
		"fonts.googleapis.com",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	// The only </script> closings are our own three tags.
	if n := strings.Count(strings.ToLower(s), "</script>"); n != 2 {
		t.Errorf("want 2 closing script tags, got %d", n)
	}
	if strings.Contains(s, "/files/") {
		t.Error("uploaded file reference was not inlined")
	}
}

func TestDiagramDecksGetFullBundle(t *testing.T) {
	d := strings.Replace(deckJSON, `{"id":"h","type":"heading","props":{"text":"</script><b>hi"}}`,
		`{"id":"h","type":"diagram","props":{"source":"flowchart LR\n A-->B"}}`, 1)
	out, err := exporter(t).HTML(context.Background(), []byte(d))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "/*full*/") {
		t.Fatal("diagram deck should use the full bundle")
	}
}

func TestMissingAssetFails(t *testing.T) {
	d := strings.Replace(deckJSON, "f_00000000000000000001", "f_00000000000000000002", 1)
	if _, err := exporter(t).HTML(context.Background(), []byte(d)); err == nil {
		t.Fatal("expected error for missing asset")
	}
}

func TestFilename(t *testing.T) {
	if got := export.Filename("  Q3 Review: Plan & Ask!  "); got != "q3-review-plan-ask.html" {
		t.Fatal(got)
	}
	if got := export.Filename("???"); got != "deck.html" {
		t.Fatal(got)
	}
}
