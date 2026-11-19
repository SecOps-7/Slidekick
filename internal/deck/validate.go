package deck

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaURL = "https://slidekick.dev/schema/deck.schema.json"

// Validator checks decks against the JSON Schema plus the semantic rules the
// schema cannot express (layout regions, id uniqueness, known theme).
type Validator struct {
	schema *jsonschema.Schema
	cat    *Catalog
	themes *ThemeSet
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// NewValidator compiles schema/deck.schema.json from the embedded FS.
func NewValidator(schemaFS fs.FS, cat *Catalog, themes *ThemeSet) (*Validator, error) {
	raw, err := fs.ReadFile(schemaFS, "schema/deck.schema.json")
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaURL, doc); err != nil {
		return nil, err
	}
	sch, err := c.Compile(schemaURL)
	if err != nil {
		return nil, err
	}
	return &Validator{schema: sch, cat: cat, themes: themes}, nil
}

// ValidationError lists every problem found, one per line, in terms an agent
// can act on.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return "deck is invalid:\n- " + strings.Join(e.Problems, "\n- ")
}

// Validate checks a raw deck document and returns the parsed deck.
func (v *Validator) Validate(raw []byte) (*Deck, error) {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, &ValidationError{Problems: []string{"not valid JSON: " + err.Error()}}
	}
	if err := v.schema.Validate(inst); err != nil {
		return nil, &ValidationError{Problems: schemaProblems(err)}
	}
	d, err := Parse(raw)
	if err != nil {
		return nil, &ValidationError{Problems: []string{err.Error()}}
	}
	if p := v.semantic(d); len(p) > 0 {
		return nil, &ValidationError{Problems: p}
	}
	return d, nil
}

func (v *Validator) semantic(d *Deck) []string {
	var p []string
	if _, ok := v.themes.Themes[d.Theme]; !ok {
		p = append(p, fmt.Sprintf("/theme: unknown theme %q (available: %s)", d.Theme, strings.Join(v.themes.Names(), ", ")))
	}
	slideIDs := map[string]int{}
	for i, s := range d.Slides {
		if j, dup := slideIDs[s.ID]; dup {
			p = append(p, fmt.Sprintf("/slides/%d/id: %q is already used by slide %d; slide ids must be unique", i, s.ID, j))
		}
		slideIDs[s.ID] = i
		allowed := map[string]bool{}
		for _, r := range v.cat.LayoutRegions(s.Layout) {
			allowed[r] = true
		}
		elIDs := map[string]bool{}
		for _, r := range v.cat.RegionOrder(s.Layout, s.Regions) {
			if !allowed[r] {
				p = append(p, fmt.Sprintf("/slides/%d/regions/%s: layout %q has no region %q (regions: %s)",
					i, r, s.Layout, r, strings.Join(v.cat.LayoutRegions(s.Layout), ", ")))
			}
			for k, e := range s.Regions[r] {
				if elIDs[e.ID] {
					p = append(p, fmt.Sprintf("/slides/%d/regions/%s/%d/id: %q is used twice on this slide", i, r, k, e.ID))
				}
				elIDs[e.ID] = true
			}
		}
	}
	return p
}

// schemaProblems flattens a jsonschema error tree into leaf messages with
// their instance paths.
func schemaProblems(err error) []string {
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []string{err.Error()}
	}
	var out []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			loc := "/" + strings.Join(e.InstanceLocation, "/")
			msg := e.Error()
			// The library prefixes messages with location info; keep the last line.
			if i := strings.LastIndex(msg, "\n"); i >= 0 {
				msg = msg[i+1:]
			}
			msg = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(msg), "-"))
			if j := strings.Index(msg, ": "); j >= 0 && strings.HasPrefix(msg, "at ") {
				msg = msg[j+2:]
			}
			out = append(out, loc+": "+msg)
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	if len(out) > 20 {
		out = append(out[:20], fmt.Sprintf("...and %d more", len(out)-20))
	}
	return out
}
