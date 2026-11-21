package tts

import (
	"bytes"
	"testing"
	"time"
)

// fakeMP3 builds n MPEG-1 Layer III frames at 128 kbit/s, 44.1 kHz
// (417 bytes each, 1152 samples), optionally behind an ID3v2 tag.
func fakeMP3(n int, id3 bool) []byte {
	var b bytes.Buffer
	if id3 {
		b.Write([]byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, 20})
		b.Write(make([]byte, 20))
	}
	for i := 0; i < n; i++ {
		f := make([]byte, 417)
		copy(f, []byte{0xFF, 0xFB, 0x90, 0x00})
		for j := 4; j < len(f); j++ {
			f[j] = byte(j * 7) // payload noise, may contain 0xFF bytes
		}
		b.Write(f)
	}
	return b.Bytes()
}

func near(got, want time.Duration) bool {
	d := got - want
	return d > -5*time.Millisecond && d < 5*time.Millisecond
}

func TestMP3Duration(t *testing.T) {
	frame := time.Second * 1152 / 44100
	cases := []struct {
		name string
		data []byte
		want time.Duration
	}{
		{"cbr", fakeMP3(100, false), 100 * frame},
		{"id3", fakeMP3(383, true), 383 * frame},
		{"empty", nil, 0},
		{"garbage", []byte("not an mp3 at all, just text"), 0},
		{"joined", append(fakeMP3(10, true), fakeMP3(20, true)...), 30 * frame},
	}
	for _, c := range cases {
		if got := MP3Duration(c.data); !near(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// A truncated last frame still counts.
	b := fakeMP3(10, false)
	if got := MP3Duration(b[:len(b)-100]); !near(got, 10*frame) {
		t.Errorf("truncated: %v", got)
	}
}

func TestParseFrameMPEG2(t *testing.T) {
	// MPEG-2 Layer III, 64 kbit/s, 24 kHz: 576 samples, 72*64000/24000 = 192 bytes.
	f, ok := parseFrame([]byte{0xFF, 0xF3, 0x84, 0x00})
	if !ok || f.samples != 576 || f.rate != 24000 || f.size != 192 {
		t.Fatalf("%+v %v", f, ok)
	}
	if _, ok := parseFrame([]byte{0xFF, 0xFB, 0xF0, 0}); ok {
		t.Fatal("bad bitrate index accepted")
	}
}

func TestStripID3(t *testing.T) {
	b := fakeMP3(1, true)
	if s := StripID3(b); len(s) != 417 || s[0] != 0xFF {
		t.Fatalf("len %d", len(s))
	}
}
