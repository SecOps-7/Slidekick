package main

import "testing"

func TestCastBase(t *testing.T) {
	for _, c := range []struct{ base, want string }{
		{"http://192.168.1.10:7410", ""},                          // plain HTTP: as is
		{"https://192.168.1.10:7443", "http://192.168.1.10:7410"}, // the local HTTPS port: cast over HTTP
		{"https://slides.example.com", ""},                        // a proxy with a trusted certificate: as is
		{"https://slides.example.com:8443", ""},                   // another port: as is
		{"https://[fd00::10]:7443", "http://[fd00::10]:7410"},     // IPv6
		{"not a url", ""},
	} {
		if got := castBase(c.base, ":7410", ":7443"); got != c.want {
			t.Errorf("castBase(%q) = %q, want %q", c.base, got, c.want)
		}
	}
}
