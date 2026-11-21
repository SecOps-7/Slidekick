// Package casttest is a fake Cast device for tests: a TLS listener that
// speaks the framed Cast v2 protocol well enough for internal/cast to launch
// an app, hand it a URL and stop it. Tests never touch a real device.
package casttest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Device is a running fake.
type Device struct {
	Host string
	Port int

	// LaunchError, when set, makes LAUNCH fail with this reason.
	LaunchError string

	ln       net.Listener
	mu       sync.Mutex
	app      map[string]any // the running app, nil when idle
	seq      int
	launched []string
	loaded   []map[string]any // messages received on app namespaces
	stopped  []string
	pings    int
}

// New starts a fake device on 127.0.0.1 and stops it when the test ends.
func New(t testing.TB) *Device {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}})
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	d := &Device{ln: ln, Host: host}
	d.Port, _ = strconv.Atoi(port)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go d.serve(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return d
}

// Addr is host:port.
func (d *Device) Addr() string { return net.JoinHostPort(d.Host, strconv.Itoa(d.Port)) }

// Launched lists the app ids LAUNCH was asked for.
func (d *Device) Launched() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.launched...)
}

// Loaded lists the payloads sent to running apps (e.g. DashCast URLs).
func (d *Device) Loaded() []map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]map[string]any(nil), d.loaded...)
}

// Stopped lists the session ids STOP ended.
func (d *Device) Stopped() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.stopped...)
}

// Running is the app id currently running ("" when idle).
func (d *Device) Running() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.app == nil {
		return ""
	}
	return d.app["appId"].(string)
}

type frame struct{ src, dst, ns, payload string }

func (d *Device) serve(c net.Conn) {
	defer c.Close()
	var wmu sync.Mutex
	reply := func(f frame, v any) {
		b, _ := json.Marshal(v)
		wmu.Lock()
		defer wmu.Unlock()
		writeFrame(c, frame{src: f.dst, dst: f.src, ns: f.ns, payload: string(b)})
	}
	for {
		c.SetReadDeadline(time.Now().Add(30 * time.Second))
		f, err := readFrame(c)
		if err != nil {
			return
		}
		var m map[string]any
		if json.Unmarshal([]byte(f.payload), &m) != nil {
			continue
		}
		typ, _ := m["type"].(string)
		reqID := m["requestId"]
		switch f.ns {
		case "urn:x-cast:com.google.cast.tp.connection":
			// CONNECT / CLOSE: nothing to track.
		case "urn:x-cast:com.google.cast.tp.heartbeat":
			if typ == "PING" {
				d.mu.Lock()
				d.pings++
				d.mu.Unlock()
				reply(f, map[string]any{"type": "PONG"})
			}
		case "urn:x-cast:com.google.cast.receiver":
			d.mu.Lock()
			switch typ {
			case "LAUNCH":
				appID, _ := m["appId"].(string)
				d.launched = append(d.launched, appID)
				if d.LaunchError != "" {
					d.mu.Unlock()
					reply(f, map[string]any{"type": "LAUNCH_ERROR", "reason": d.LaunchError, "requestId": reqID})
					continue
				}
				if d.app == nil || d.app["appId"] != appID {
					d.seq++
					d.app = map[string]any{"appId": appID, "displayName": "Fake app", "sessionId": fmt.Sprintf("session-%d", d.seq),
						"transportId": fmt.Sprintf("web-%d", d.seq), "namespaces": []any{map[string]any{"name": "urn:x-cast:com.madmod.dashcast"}}}
				}
			case "STOP":
				sid, _ := m["sessionId"].(string)
				if d.app == nil || d.app["sessionId"] != sid {
					d.mu.Unlock()
					reply(f, map[string]any{"type": "INVALID_REQUEST", "reason": "INVALID_SESSION_ID", "requestId": reqID})
					continue
				}
				d.stopped = append(d.stopped, sid)
				d.app = nil
			case "GET_STATUS":
			default:
				d.mu.Unlock()
				reply(f, map[string]any{"type": "INVALID_REQUEST", "reason": "INVALID_COMMAND", "requestId": reqID})
				continue
			}
			apps := []any{}
			if d.app != nil {
				apps = append(apps, d.app)
			}
			d.mu.Unlock()
			reply(f, map[string]any{"type": "RECEIVER_STATUS", "requestId": reqID, "status": map[string]any{"applications": apps, "volume": map[string]any{"level": 1}}})
		default:
			// A message to a running app.
			d.mu.Lock()
			if d.app != nil && f.dst == d.app["transportId"] {
				m["_namespace"] = f.ns
				d.loaded = append(d.loaded, m)
			}
			d.mu.Unlock()
		}
	}
}

// ---- a second, independent implementation of the wire format ----

func putField(b []byte, field int, s string) []byte {
	b = binary.AppendUvarint(b, uint64(field<<3|2))
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

func writeFrame(w io.Writer, f frame) error {
	var b []byte
	b = append(b, 1<<3, 0) // protocol_version = CASTV2_1_0
	b = putField(b, 2, f.src)
	b = putField(b, 3, f.dst)
	b = putField(b, 4, f.ns)
	b = append(b, 5<<3, 0) // payload_type = STRING
	b = putField(b, 6, f.payload)
	out := binary.BigEndian.AppendUint32(nil, uint32(len(b)))
	_, err := w.Write(append(out, b...))
	return err
}

func readFrame(r io.Reader) (frame, error) {
	var f frame
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return f, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > 1<<16 {
		return f, fmt.Errorf("frame too large")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return f, err
	}
	for len(b) > 0 {
		tag, k := binary.Uvarint(b)
		if k <= 0 {
			return f, fmt.Errorf("bad tag")
		}
		b = b[k:]
		if tag&7 == 0 {
			_, k = binary.Uvarint(b)
			if k <= 0 {
				return f, fmt.Errorf("bad varint")
			}
			b = b[k:]
			continue
		}
		l, k := binary.Uvarint(b)
		if k <= 0 || l > uint64(len(b)-k) {
			return f, fmt.Errorf("bad length")
		}
		v := string(b[k : k+int(l)])
		b = b[k+int(l):]
		switch tag >> 3 {
		case 2:
			f.src = v
		case 3:
			f.dst = v
		case 4:
			f.ns = v
		case 6:
			f.payload = v
		}
	}
	return f, nil
}

func selfSigned(t testing.TB) tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake-chromecast"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
