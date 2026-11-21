package cast

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// The Cast v2 wire format: every message is a 4-byte big-endian length
// followed by a protobuf CastMessage (proto2, from Chromium's
// cast_channel.proto):
//
//	message CastMessage {
//	  required ProtocolVersion protocol_version = 1; // CASTV2_1_0 = 0
//	  required string source_id = 2;
//	  required string destination_id = 3;
//	  required string namespace = 4;
//	  required PayloadType payload_type = 5;         // STRING = 0, BINARY = 1
//	  optional string payload_utf8 = 6;
//	  optional bytes payload_binary = 7;
//	}
//
// Seven scalar fields don't justify a protobuf runtime and generated code,
// so this file encodes them by hand with varints from encoding/binary.

// maxFrame caps one incoming message; Cast messages are small JSON documents
// (a receiver status is a few KB).
const maxFrame = 64 << 10

type message struct {
	Source, Dest, Namespace string
	Payload                 string // payload_utf8 (all namespaces used here are JSON)
	Binary                  []byte // payload_binary, kept only to skip it cleanly
}

const (
	wireVarint = 0
	wireBytes  = 2
)

func appendTag(b []byte, field, wt int) []byte {
	return binary.AppendUvarint(b, uint64(field<<3|wt))
}

func appendString(b []byte, field int, s string) []byte {
	b = appendTag(b, field, wireBytes)
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

func (m *message) marshal() []byte {
	b := make([]byte, 0, 64+len(m.Payload))
	b = appendTag(b, 1, wireVarint)
	b = binary.AppendUvarint(b, 0) // CASTV2_1_0
	b = appendString(b, 2, m.Source)
	b = appendString(b, 3, m.Dest)
	b = appendString(b, 4, m.Namespace)
	b = appendTag(b, 5, wireVarint)
	b = binary.AppendUvarint(b, 0) // STRING
	b = appendString(b, 6, m.Payload)
	return b
}

var errMalformed = errors.New("cast: malformed message")

func unmarshal(b []byte) (*message, error) {
	m := &message{}
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, errMalformed
		}
		b = b[n:]
		field, wt := int(tag>>3), int(tag&7)
		switch wt {
		case wireVarint:
			if _, n = binary.Uvarint(b); n <= 0 {
				return nil, errMalformed
			}
			b = b[n:]
		case wireBytes:
			l, n := binary.Uvarint(b)
			if n <= 0 || l > uint64(len(b)-n) {
				return nil, errMalformed
			}
			v := b[n : n+int(l)]
			b = b[n+int(l):]
			switch field {
			case 2:
				m.Source = string(v)
			case 3:
				m.Dest = string(v)
			case 4:
				m.Namespace = string(v)
			case 6:
				m.Payload = string(v)
			case 7:
				m.Binary = append([]byte(nil), v...)
			}
		default:
			return nil, fmt.Errorf("cast: unsupported wire type %d", wt)
		}
	}
	return m, nil
}

func writeFrame(w io.Writer, m *message) error {
	body := m.marshal()
	buf := make([]byte, 4, 4+len(body))
	binary.BigEndian.PutUint32(buf, uint32(len(body)))
	_, err := w.Write(append(buf, body...))
	return err
}

func readFrame(r io.Reader) (*message, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxFrame {
		return nil, fmt.Errorf("cast: message of %d bytes is too large", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return unmarshal(body)
}
