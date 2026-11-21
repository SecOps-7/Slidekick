package about

import (
	"testing"
	"time"
)

func TestReleases(t *testing.T) {
	md := "# Changelog\n\n## 1.1.0 (2026-12-01)\n\nIntro prose.\n\n### Voices\n- Fish Audio and **ElevenLabs** voices, see [docs](docs/x.md).\n  - nested detail is skipped\n- `present_slide` tool.\n\n### Casting\n- Chromecast.\n\n## v1.0.0\n- Before any section.\n"
	rs := Releases(md)
	if len(rs) != 2 || rs[0].Version != "1.1.0" || rs[0].Title != "2026-12-01" || rs[1].Version != "1.0.0" {
		t.Fatalf("releases: %+v", rs)
	}
	n := rs[0].Notes
	if len(n) != 3 || n[0].Section != "Voices" || n[0].Text != "Fish Audio and ElevenLabs voices, see docs." || n[1].Text != "present_slide tool." || n[2].Section != "Casting" {
		t.Fatalf("notes: %+v", n)
	}
	if len(rs[1].Notes) != 1 || rs[1].Notes[0].Section != "" {
		t.Fatalf("no section: %+v", rs[1].Notes)
	}
	if got := Releases("no headings here\n- stray"); len(got) != 0 {
		t.Fatalf("stray: %+v", got)
	}
}

func TestCollect(t *testing.T) {
	i := Collect(Build{Version: "1.0.0", Started: time.Now().Add(-90 * time.Second), Changelog: "## 1.0.0\n- x\n"}, 7)
	if i.Version != "1.0.0" || i.Runtime.Go == "" || i.Runtime.UptimeSec < 89 || i.Database.Decks != 7 || len(i.Releases) != 1 {
		t.Fatalf("%+v", i)
	}
	for _, d := range i.Deps {
		if d.Licence == "" || d.Kind != "go" {
			t.Fatalf("dep %+v", d)
		}
	}
}
