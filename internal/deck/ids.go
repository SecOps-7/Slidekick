package deck

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

var idEncoding = base32.NewEncoding("abcdefghijkmnpqrstuvwxyz23456789").WithPadding(base32.NoPadding)

// NewID returns a short random id with the given prefix, e.g. "s_k3m9x2pq".
func NewID(prefix string) string {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + strings.ToLower(idEncoding.EncodeToString(b[:]))
}

// NewLongID returns an id with 80 random bits, for ids that act as share links.
func NewLongID(prefix string) string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + strings.ToLower(idEncoding.EncodeToString(b[:]))
}

var elementTypes = map[string]bool{
	"heading": true, "text": true, "bullets": true, "image": true,
	"code": true, "stat": true, "quote": true,
	"chart": true, "diagram": true, "icon": true, "shape": true, "embed": true,
}

// AssignIDs walks a decoded JSON value and gives every slide-shaped object
// (has "layout") and element-shaped object (has a known "type" and "props")
// an id if it lacks one. It mutates v in place and returns it.
func AssignIDs(v any) any {
	switch t := v.(type) {
	case map[string]any:
		_, isSlide := t["layout"]
		typ, _ := t["type"].(string)
		_, hasProps := t["props"]
		isElement := elementTypes[typ] && hasProps
		if id, _ := t["id"].(string); id == "" {
			switch {
			case isSlide:
				t["id"] = NewID("s")
			case isElement:
				t["id"] = NewID("e")
			}
		}
		for k, c := range t {
			if k == "props" { // never rewrite inside element props
				continue
			}
			t[k] = AssignIDs(c)
		}
	case []any:
		for i := range t {
			t[i] = AssignIDs(t[i])
		}
	}
	return v
}
