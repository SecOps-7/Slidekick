// Package about answers "what is this install": the build, what it runs on,
// the release notes shipped with it and the Go modules compiled into it.
// Everything is read locally; nothing here calls out to the internet.
package about

import (
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"
)

// Info is the about page's data. Empty fields are left out: a local build has
// no commit or build date, and the page skips what it doesn't have.
type Info struct {
	Version  string    `json:"version"`
	Commit   string    `json:"commit,omitempty"`
	Built    string    `json:"built,omitempty"`
	Runtime  Runtime   `json:"runtime"`
	Database Database  `json:"database"`
	Releases []Release `json:"releases"`
	Deps     []Dep     `json:"deps"`
	Licence  string    `json:"licence"`
}

type Runtime struct {
	Go        string `json:"go"`
	Platform  string `json:"platform"`
	MemoryMB  int    `json:"memoryMb"`
	Kernel    string `json:"kernel,omitempty"`
	UptimeSec int64  `json:"uptimeSec"`
}

type Database struct {
	Engine string `json:"engine"`
	Decks  int    `json:"decks"`
}

// Release is one version's notes; each note carries the changelog section it
// sits under ("Agents (MCP)", "Decks"...).
type Release struct {
	Version string `json:"version"`
	Title   string `json:"title,omitempty"`
	Notes   []Note `json:"notes"`
}

type Note struct {
	Section string `json:"section"`
	Text    string `json:"text"`
}

// Dep is one library or asset shipped in the build.
type Dep struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Licence string `json:"licence"`
	URL     string `json:"url,omitempty"`
	Kind    string `json:"kind"` // "go", "web" or "font"
}

// Build is what the binary knows about itself.
type Build struct {
	Version, Commit, Built, Changelog, Licence string
	Started                                    time.Time
}

// Collect assembles the page's data.
func Collect(b Build, decks int) Info {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return Info{
		Version: b.Version,
		Commit:  b.Commit,
		Built:   b.Built,
		Runtime: Runtime{
			Go:        runtime.Version(),
			Platform:  runtime.GOOS + "/" + runtime.GOARCH,
			MemoryMB:  int(mem.Sys / 1024 / 1024),
			Kernel:    kernel(),
			UptimeSec: int64(time.Since(b.Started).Seconds()),
		},
		Database: Database{Engine: "SQLite", Decks: decks},
		Releases: Releases(b.Changelog),
		Deps:     GoDeps(),
		Licence:  b.Licence,
	}
}

func kernel() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

var (
	releaseRe = regexp.MustCompile(`^##\s+v?([0-9][^\s(]*)\s*(?:\((.*)\))?\s*$`)
	sectionRe = regexp.MustCompile(`^###\s+(.+?)\s*$`)
	linkRe    = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
)

// Releases parses CHANGELOG.md: "## 1.0.0 (unreleased)" starts a release,
// "### Section" a section, and top-level "- " bullets are the notes. Nested
// bullets and prose are left to the file itself.
func Releases(md string) []Release {
	out := []Release{}
	cur := -1
	section := ""
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		if m := releaseRe.FindStringSubmatch(line); m != nil {
			out = append(out, Release{Version: m[1], Title: m[2], Notes: []Note{}})
			cur, section = len(out)-1, ""
			continue
		}
		if cur < 0 {
			continue
		}
		if m := sectionRe.FindStringSubmatch(line); m != nil {
			section = plain(m[1])
			continue
		}
		if strings.HasPrefix(line, "- ") {
			out[cur].Notes = append(out[cur].Notes, Note{Section: section, Text: plain(line[2:])})
		}
	}
	return out
}

// plain drops the Markdown marks a note is written with.
func plain(s string) string {
	s = linkRe.ReplaceAllString(s, "$1")
	s = strings.NewReplacer("**", "", "`", "").Replace(s)
	return strings.TrimSpace(s)
}

// moduleLicences are the licences of the modules go.mod requires directly;
// only these are listed (their own dependencies are in go.sum).
var moduleLicences = map[string]string{
	"github.com/chromedp/chromedp":             "MIT",
	"github.com/coder/websocket":               "ISC",
	"github.com/coreos/go-oidc/v3":             "Apache-2.0",
	"github.com/evanphx/json-patch/v5":         "BSD-3-Clause",
	"github.com/minio/minio-go/v7":             "Apache-2.0",
	"github.com/modelcontextprotocol/go-sdk":   "MIT",
	"github.com/santhosh-tekuri/jsonschema/v6": "Apache-2.0",
	"golang.org/x/net":                         "BSD-3-Clause",
	"golang.org/x/oauth2":                      "BSD-3-Clause",
	"modernc.org/sqlite":                       "BSD-3-Clause",
}

// GoDeps lists the directly required Go modules compiled into this binary.
func GoDeps() []Dep {
	out := []Dep{}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return out
	}
	for _, m := range info.Deps {
		lic, direct := moduleLicences[m.Path]
		if !direct {
			continue
		}
		v := m.Version
		if m.Replace != nil && m.Replace.Version != "" {
			v = m.Replace.Version
		}
		out = append(out, Dep{Name: m.Path, Version: strings.TrimPrefix(v, "v"), Licence: lic, URL: moduleURL(m.Path), Kind: "go"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func moduleURL(path string) string {
	if strings.HasPrefix(path, "github.com/") {
		if parts := strings.SplitN(path, "/", 4); len(parts) >= 3 {
			return "https://" + strings.Join(parts[:3], "/")
		}
	}
	return "https://pkg.go.dev/" + path
}
