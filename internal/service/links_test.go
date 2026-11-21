package service

import (
	"testing"
	"time"
)

func TestCheckLink(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	const key, id = "0123456789abcdef01234567", "d_abc"
	tok := linkToken(key, LinkPresent, id, now.Add(time.Hour))

	cases := []struct {
		name          string
		key, kind, id string
		k             string
		at            time.Time
		ok            bool
	}{
		{"the key itself never expires", key, LinkPresent, id, key, now.Add(1000 * time.Hour), true},
		{"a token before its expiry", key, LinkPresent, id, tok, now.Add(59 * time.Minute), true},
		{"a token at its expiry", key, LinkPresent, id, tok, now.Add(time.Hour), false},
		{"a token for another kind", key, LinkView, id, tok, now, false},
		{"a token for another deck", key, LinkPresent, "d_other", tok, now, false},
		{"a token after the key was replaced", "fedcba9876543210fedcba98", LinkPresent, id, tok, now, false},
		{"a token with its expiry pushed back", key, LinkPresent, id, "zzzzzz" + tok[len(tok)-25:], now, false},
		{"a made-up token", key, LinkPresent, id, "abc.0123", now, false},
		{"no key", "", LinkPresent, id, "", now, false},
	}
	for _, c := range cases {
		ok, exp := checkLink(c.key, c.kind, c.id, c.k, c.at)
		if ok != c.ok {
			t.Errorf("%s: ok %v, want %v", c.name, ok, c.ok)
		}
		if ok && c.k == tok && !exp.Equal(now.Add(time.Hour)) {
			t.Errorf("%s: expiry %v", c.name, exp)
		}
	}
}
