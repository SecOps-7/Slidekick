package llm

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// maxSSEEvent bounds one event's data, so a broken or hostile server cannot
// make the reader buffer without limit.
const maxSSEEvent = 8 << 20

type sseEvent struct {
	Event string
	Data  string
}

// readSSE calls fn for every event in a text/event-stream body until EOF,
// a read error, or fn returns an error (returned as-is; errStopSSE ends
// cleanly).
func readSSE(r io.Reader, fn func(sseEvent) error) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var (
		event string
		data  strings.Builder
		have  bool
	)
	dispatch := func() error {
		if !have {
			event = ""
			return nil
		}
		ev := sseEvent{Event: event, Data: data.String()}
		event, have = "", false
		data.Reset()
		return fn(ev)
	}
	for {
		line, err := readLine(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				if derr := dispatch(); derr != nil && !errors.Is(derr, errStopSSE) {
					return derr
				}
				return nil
			}
			return err
		}
		if line == "" {
			if derr := dispatch(); derr != nil {
				if errors.Is(derr, errStopSSE) {
					return nil
				}
				return derr
			}
			continue
		}
		if line[0] == ':' {
			continue // comment / keep-alive
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			if have {
				data.WriteByte('\n')
			}
			data.WriteString(value)
			have = true
			if data.Len() > maxSSEEvent {
				return errors.New("llm: stream event too large")
			}
		}
	}
}

var errStopSSE = errors.New("stop")

// readLine reads one line without its CR/LF terminator, bounded in size.
func readLine(br *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		frag, isPrefix, err := br.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) && sb.Len() > 0 {
				return sb.String(), nil
			}
			return "", err
		}
		sb.Write(frag)
		if sb.Len() > maxSSEEvent {
			return "", errors.New("llm: stream line too large")
		}
		if !isPrefix {
			return sb.String(), nil
		}
	}
}
