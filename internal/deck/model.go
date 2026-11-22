// Package deck holds the deck model, the layout and theme catalogues,
// schema validation, id assignment and migrations. It has no I/O beyond
// reading the embedded catalogue files.
package deck

import "encoding/json"

// CurrentSchemaVersion is the deck schema version this build writes.
const CurrentSchemaVersion = 1

// Deck is the typed view of a deck document. The canonical form is the raw
// JSON stored per version; this struct is for reading, linting and building.
type Deck struct {
	SchemaVersion int     `json:"schemaVersion"`
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Theme         string  `json:"theme"`
	Aspect        string  `json:"aspect"`
	Slides        []Slide `json:"slides"`
}

type Slide struct {
	ID         string               `json:"id"`
	Layout     string               `json:"layout"`
	Regions    map[string][]Element `json:"regions"`
	Transition *Transition          `json:"transition,omitempty"`
	Notes      string               `json:"notes,omitempty"`
	Background *Background          `json:"background,omitempty"`
	// HideUpdated hides a live slide's last-updated time.
	HideUpdated bool `json:"hideUpdated,omitempty"`
}

type Transition struct {
	Type     string `json:"type"`
	Duration string `json:"duration,omitempty"`
}

type Background struct {
	Token string   `json:"token,omitempty"`
	Image string   `json:"image,omitempty"`
	Dim   *float64 `json:"dim,omitempty"`
}

type Element struct {
	ID    string         `json:"id"`
	Type  string         `json:"type"`
	Props map[string]any `json:"props"`
	Style *Style         `json:"style,omitempty"`
	Build *Build         `json:"build,omitempty"`
}

type Style struct {
	Color    string `json:"color,omitempty"`
	Size     string `json:"size,omitempty"`
	Align    string `json:"align,omitempty"`
	Weight   string `json:"weight,omitempty"`
	ColorRaw string `json:"colorRaw,omitempty"`
}

type Build struct {
	Step   int    `json:"step"`
	Preset string `json:"preset,omitempty"`
}

// Op is one RFC 6902 JSON Patch operation.
type Op struct {
	Op    string `json:"op" jsonschema:"one of add, remove, replace, move, copy, test"`
	Path  string `json:"path" jsonschema:"JSON Pointer, e.g. /slides/0/regions/body/1/props/text"`
	From  string `json:"from,omitempty" jsonschema:"source pointer for move and copy"`
	Value any    `json:"value,omitempty" jsonschema:"value for add, replace and test"`
}

// Parse decodes a raw deck document.
func Parse(raw []byte) (*Deck, error) {
	var d Deck
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// SlideIndex returns the index of the slide with the given id, or -1.
func (d *Deck) SlideIndex(id string) int {
	for i, s := range d.Slides {
		if s.ID == id {
			return i
		}
	}
	return -1
}

// Elements returns the slide's elements in a stable order: by the layout's
// region order, then any unknown regions alphabetically.
func (s *Slide) Elements(cat *Catalog) []Element {
	var out []Element
	for _, r := range cat.RegionOrder(s.Layout, s.Regions) {
		out = append(out, s.Regions[r]...)
	}
	return out
}

// Str reads a string prop.
func (e *Element) Str(key string) string {
	if v, ok := e.Props[key].(string); ok {
		return v
	}
	return ""
}

// Items reads the bullets prop.
func (e *Element) Items() []string {
	raw, _ := e.Props["items"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
