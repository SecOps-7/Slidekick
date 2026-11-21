// Package cast drives Chromecast devices over the LAN with the Cast v2
// protocol: TLS on TCP 8009, length-prefixed protobuf CastMessages carrying
// JSON on the connection, heartbeat and receiver namespaces. It is just
// enough to launch the DashCast receiver, hand it a URL and stop it again.
package cast

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	nsConnection = "urn:x-cast:com.google.cast.tp.connection"
	nsHeartbeat  = "urn:x-cast:com.google.cast.tp.heartbeat"
	nsReceiver   = "urn:x-cast:com.google.cast.receiver"

	senderID   = "sender-slidekick"
	receiverID = "receiver-0"

	// DefaultPort is where every Cast device listens.
	DefaultPort = 8009
)

// Timeouts. A Cast exchange is a handful of small messages on the LAN, so
// anything slower than this means the device is gone or busy.
const (
	dialTimeout   = 5 * time.Second
	writeTimeout  = 5 * time.Second
	pingEvery     = 5 * time.Second
	idleTimeout   = 20 * time.Second // no traffic at all (not even PONG) for this long: give up
	launchTimeout = 20 * time.Second // an app launch on an older device can take ~10 s
)

// Application is a running receiver app, from RECEIVER_STATUS.
type Application struct {
	AppID       string `json:"appId"`
	DisplayName string `json:"displayName"`
	SessionID   string `json:"sessionId"`
	TransportID string `json:"transportId"`
	StatusText  string `json:"statusText"`
}

// ReceiverStatus is the part of RECEIVER_STATUS this package uses.
type ReceiverStatus struct {
	Applications []Application `json:"applications"`
}

// App returns the running app with this id, or nil.
func (st *ReceiverStatus) App(appID string) *Application {
	for i := range st.Applications {
		if st.Applications[i].AppID == appID {
			return &st.Applications[i]
		}
	}
	return nil
}

// Client is one connection to a Cast device.
type Client struct {
	conn    net.Conn
	wmu     sync.Mutex
	nextID  atomic.Int64
	mu      sync.Mutex
	waiters map[int64]chan json.RawMessage
	done    chan struct{}
	err     error // why done closed
	once    sync.Once
}

// Dial connects to a Cast device (host:port) and opens the virtual
// connection to its receiver.
func Dial(ctx context.Context, addr string) (*Client, error) {
	d := tls.Dialer{
		NetDialer: &net.Dialer{Timeout: dialTimeout},
		// Cast devices present a certificate chained to Google's device CA,
		// issued for the device's serial rather than any hostname or IP, so it
		// can't be checked the way web certificates are. Every Cast sender
		// (Chrome, pychromecast, go-chromecast) skips verification for the
		// same reason. Real device authentication would be the separate
		// deviceauth challenge, which isn't worth it for showing slides on
		// the LAN: the worst a spoofed device learns is the deck's view link,
		// which the admin entered it to receive.
		Config: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, //nolint:gosec // see above
	}
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, err := d.DialContext(dctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("can't reach the cast device at %s: %w", addr, err)
	}
	c := &Client{conn: conn, waiters: map[int64]chan json.RawMessage{}, done: make(chan struct{})}
	if err := c.send(nsConnection, receiverID, map[string]any{"type": "CONNECT"}); err != nil {
		conn.Close()
		return nil, err
	}
	go c.readLoop()
	go c.heartbeat()
	return c, nil
}

// Close ends the connection (the receiver app keeps running).
func (c *Client) Close() error {
	c.send(nsConnection, receiverID, map[string]any{"type": "CLOSE"})
	c.shut(errors.New("cast: connection closed"))
	return nil
}

func (c *Client) shut(err error) {
	c.once.Do(func() {
		c.err = err
		close(c.done)
		c.conn.Close()
	})
}

func (c *Client) send(ns, dest string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := writeFrame(c.conn, &message{Source: senderID, Dest: dest, Namespace: ns, Payload: string(b)}); err != nil {
		c.shut(err)
		return fmt.Errorf("cast: send: %w", err)
	}
	return nil
}

func (c *Client) readLoop() {
	for {
		c.conn.SetReadDeadline(time.Now().Add(idleTimeout))
		m, err := readFrame(c.conn)
		if err != nil {
			c.shut(fmt.Errorf("cast: connection lost: %w", err))
			return
		}
		var head struct {
			Type      string `json:"type"`
			RequestID int64  `json:"requestId"`
		}
		if json.Unmarshal([]byte(m.Payload), &head) != nil {
			continue
		}
		switch {
		case m.Namespace == nsHeartbeat && head.Type == "PING":
			go c.send(nsHeartbeat, m.Source, map[string]any{"type": "PONG"})
		case m.Namespace == nsConnection && head.Type == "CLOSE" && m.Source == receiverID:
			c.shut(errors.New("cast: the device closed the connection"))
			return
		case head.RequestID > 0:
			c.mu.Lock()
			ch := c.waiters[head.RequestID]
			delete(c.waiters, head.RequestID)
			c.mu.Unlock()
			if ch != nil {
				ch <- json.RawMessage(m.Payload)
			}
		}
	}
}

func (c *Client) heartbeat() {
	t := time.NewTicker(pingEvery)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			c.send(nsHeartbeat, receiverID, map[string]any{"type": "PING"})
		}
	}
}

// request sends a receiver-namespace request and waits for the reply with
// the same requestId.
func (c *Client) request(ctx context.Context, payload map[string]any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	payload["requestId"] = id
	ch := make(chan json.RawMessage, 1)
	c.mu.Lock()
	c.waiters[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
	}()
	if err := c.send(nsReceiver, receiverID, payload); err != nil {
		return nil, err
	}
	select {
	case b := <-ch:
		return b, nil
	case <-c.done:
		return nil, c.err
	case <-ctx.Done():
		return nil, fmt.Errorf("cast: no answer to %s: %w", payload["type"], ctx.Err())
	}
}

func parseStatus(b json.RawMessage) (*ReceiverStatus, error) {
	var r struct {
		Type   string         `json:"type"`
		Reason string         `json:"reason"`
		Status ReceiverStatus `json:"status"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("cast: bad reply: %w", err)
	}
	if r.Type != "RECEIVER_STATUS" {
		if r.Reason != "" {
			return nil, fmt.Errorf("cast: the device answered %s (%s)", r.Type, r.Reason)
		}
		return nil, fmt.Errorf("cast: the device answered %s", r.Type)
	}
	return &r.Status, nil
}

// Status asks the receiver what is running.
func (c *Client) Status(ctx context.Context) (*ReceiverStatus, error) {
	b, err := c.request(ctx, map[string]any{"type": "GET_STATUS"})
	if err != nil {
		return nil, err
	}
	return parseStatus(b)
}

// Launch starts a receiver app (or returns it if it is already running) and
// waits until it has a transport id to talk to.
func (c *Client) Launch(ctx context.Context, appID string) (*Application, error) {
	ctx, cancel := context.WithTimeout(ctx, launchTimeout)
	defer cancel()
	b, err := c.request(ctx, map[string]any{"type": "LAUNCH", "appId": appID})
	if err != nil {
		return nil, err
	}
	st, err := parseStatus(b)
	for {
		if err != nil {
			return nil, err
		}
		if app := st.App(appID); app != nil && app.TransportID != "" {
			return app, nil
		}
		// Some devices answer before the app is up: poll until it is.
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("cast: app %s didn't start: %w", appID, ctx.Err())
		case <-c.done:
			return nil, c.err
		case <-time.After(300 * time.Millisecond):
		}
		st, err = c.Status(ctx)
	}
}

// Stop ends a running app session.
func (c *Client) Stop(ctx context.Context, sessionID string) error {
	b, err := c.request(ctx, map[string]any{"type": "STOP", "sessionId": sessionID})
	if err != nil {
		return err
	}
	_, err = parseStatus(b)
	return err
}

// SendApp opens a virtual connection to a running app and sends it one
// message on its namespace.
func (c *Client) SendApp(app *Application, ns string, payload any) error {
	if err := c.send(nsConnection, app.TransportID, map[string]any{"type": "CONNECT"}); err != nil {
		return err
	}
	return c.send(ns, app.TransportID, payload)
}
