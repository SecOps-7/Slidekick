package tts

import "time"

// MP3 frame header tables (ISO 11172-3 / 13818-3).
var (
	// bitrates in kbit/s: [mpeg1?][layer 1..3][index]
	mp3Bitrates = [2][3][16]int{
		{ // MPEG-2 and 2.5
			{0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256, 0},
			{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
			{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
		},
		{ // MPEG-1
			{0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448, 0},
			{0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 0},
			{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0},
		},
	}
	// sample rates by version bits (0=2.5, 2=2, 3=1)
	mp3Rates = [4][3]int{{11025, 12000, 8000}, {}, {22050, 24000, 16000}, {44100, 48000, 32000}}
)

type mp3Frame struct {
	size, samples, rate int
}

// parseFrame reads the 4-byte frame header at b[0:4].
func parseFrame(b []byte) (mp3Frame, bool) {
	if len(b) < 4 || b[0] != 0xFF || b[1]&0xE0 != 0xE0 {
		return mp3Frame{}, false
	}
	ver := int(b[1]>>3) & 3   // 0: 2.5, 1: reserved, 2: 2, 3: 1
	layer := int(b[1]>>1) & 3 // 1: III, 2: II, 3: I
	bri := int(b[2] >> 4)
	sri := int(b[2]>>2) & 3
	pad := int(b[2]>>1) & 1
	if ver == 1 || layer == 0 || bri == 0 || bri == 15 || sri == 3 {
		return mp3Frame{}, false
	}
	l := 4 - layer // 1, 2 or 3
	v1 := 0
	if ver == 3 {
		v1 = 1
	}
	br := mp3Bitrates[v1][l-1][bri] * 1000
	rate := mp3Rates[ver][sri]
	var size, samples int
	switch l {
	case 1:
		samples = 384
		size = (12*br/rate + pad) * 4
	case 2:
		samples = 1152
		size = 144*br/rate + pad
	default:
		samples = 1152
		if v1 == 0 {
			samples = 576
		}
		size = samples/8*br/rate + pad
	}
	if size < 4 {
		return mp3Frame{}, false
	}
	return mp3Frame{size: size, samples: samples, rate: rate}, true
}

// id3Size is the length of an ID3v2 tag at the start of b, or 0.
func id3Size(b []byte) int {
	if len(b) < 10 || string(b[:3]) != "ID3" {
		return 0
	}
	n := int(b[6]&0x7F)<<21 | int(b[7]&0x7F)<<14 | int(b[8]&0x7F)<<7 | int(b[9]&0x7F)
	n += 10
	if b[5]&0x10 != 0 { // footer present
		n += 10
	}
	return n
}

// MP3Duration estimates the play time of an MP3 stream by walking its frame
// headers (works for constant and variable bitrate). A header is trusted
// only when the next frame follows it, or it ends the data, so stray sync
// bytes inside audio data don't count. It returns 0 when no frames are found.
func MP3Duration(b []byte) time.Duration {
	i := id3Size(b)
	var total float64 // seconds
	frames := 0
	for i+4 <= len(b) {
		f, ok := parseFrame(b[i:])
		if ok {
			next := i + f.size
			_, nextOK := parseFrame(b[min(next, len(b)):])
			if next == len(b) || (next < len(b) && nextOK) || (frames > 0 && next > len(b)) {
				total += float64(f.samples) / float64(f.rate)
				frames++
				i = next
				continue
			}
			// Concatenated streams may put another ID3 tag between frames.
			if next < len(b) {
				if n := id3Size(b[next:]); n > 0 {
					total += float64(f.samples) / float64(f.rate)
					frames++
					i = next + n
					continue
				}
			}
		} else if n := id3Size(b[i:]); n > 0 {
			i += n
			continue
		}
		i++
	}
	return time.Duration(total * float64(time.Second))
}

// StripID3 returns b without a leading ID3v2 tag, so MP3 streams can be
// joined.
func StripID3(b []byte) []byte {
	if n := id3Size(b); n > 0 && n <= len(b) {
		return b[n:]
	}
	return b
}
