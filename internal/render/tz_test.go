package render

import (
	"context"
	"testing"
)

func TestTimeZone(t *testing.T) {
	for tz, want := range map[string]string{
		"Europe/Berlin":       "Europe/Berlin",
		"America/New_York":    "America/New_York",
		"UTC":                 "UTC",
		"":                    "",
		"Local":               "",
		"Mars/Olympus_Mons":   "",
		"../../etc/passwd":    "",
		"Europe/Berlin; rm x": "",
	} {
		if got := ValidTimeZone(tz); got != want {
			t.Errorf("ValidTimeZone(%q) = %q, want %q", tz, got, want)
		}
	}
	if v, _ := WithTimeZone(context.Background(), "Europe/Berlin").Value(tzKey{}).(string); v != "Europe/Berlin" {
		t.Errorf("valid zone not carried: %q", v)
	}
	if v := WithTimeZone(context.Background(), "nope").Value(tzKey{}); v != nil {
		t.Errorf("invalid zone carried: %v", v)
	}
}
