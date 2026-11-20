package server

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// failLimiter locks an IP out after repeated bad tokens, so short or leaked
// tokens can't be brute-forced at line speed.
type failLimiter struct {
	mu   sync.Mutex
	hits map[string]*failEntry
}

type failEntry struct {
	count       int
	windowStart time.Time
	lockedUntil time.Time
}

const (
	maxFailures  = 10
	failWindow   = time.Minute
	lockDuration = 5 * time.Minute
)

func newFailLimiter() *failLimiter { return &failLimiter{hits: map[string]*failEntry{}} }

func (l *failLimiter) allowed(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.hits[ip]
	return e == nil || time.Now().After(e.lockedUntil)
}

func (l *failLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	e := l.hits[ip]
	if e == nil || now.Sub(e.windowStart) > failWindow {
		e = &failEntry{windowStart: now}
		l.hits[ip] = e
	}
	e.count++
	if e.count >= maxFailures {
		e.lockedUntil = now.Add(lockDuration)
		e.count = 0
		e.windowStart = now
	}
	if len(l.hits) > 10000 { // bound memory
		for k, v := range l.hits {
			if now.After(v.lockedUntil) && now.Sub(v.windowStart) > failWindow {
				delete(l.hits, k)
			}
		}
	}
}

func (l *failLimiter) succeed(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, ip)
}

// connLimiter caps WebSocket connections per client IP and per deck.
type connLimiter struct {
	mu            sync.Mutex
	perIP, perDek map[string]int
	maxIP, maxDek int
}

func newConnLimiter(maxIP, maxDeck int) *connLimiter {
	return &connLimiter{perIP: map[string]int{}, perDek: map[string]int{}, maxIP: maxIP, maxDek: maxDeck}
}

func (c *connLimiter) acquire(ip, deck string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.perIP[ip] >= c.maxIP || c.perDek[deck] >= c.maxDek {
		return false
	}
	c.perIP[ip]++
	c.perDek[deck]++
	return true
}

func (c *connLimiter) release(ip, deck string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.perIP[ip]--; c.perIP[ip] <= 0 {
		delete(c.perIP, ip)
	}
	if c.perDek[deck]--; c.perDek[deck] <= 0 {
		delete(c.perDek, deck)
	}
}

// exportCache keeps recent HTML exports by deck version and limits how many
// are built at once (each inlines every uploaded file).
type exportCache struct {
	sem   chan struct{}
	mu    sync.Mutex
	order []string
	data  map[string][]byte
}

func newExportCache(concurrency, size int) *exportCache {
	return &exportCache{sem: make(chan struct{}, concurrency), data: map[string][]byte{}, order: make([]string, 0, size)}
}

func (c *exportCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.data[key]
	return b, ok
}

func (c *exportCache) put(key string, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.data[key]; ok {
		return
	}
	if len(c.order) == cap(c.order) {
		delete(c.data, c.order[0])
		c.order = c.order[1:]
	}
	c.order = append(c.order, key)
	c.data[key] = b
}

// clientIP is the connecting address. Behind a reverse proxy every client
// shares the proxy's address; limits then apply to the proxy as a whole.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
