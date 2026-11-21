package narration

import (
	"container/list"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/SecOps-7/slidekick/internal/tts"
)

// Cache keeps synthesized audio in memory, least recently used first out,
// bounded by total bytes and by how long an entry goes unused.
//
// Keys are HMAC-SHA256(provider|voice|model|text) under a random key made at
// start-up, so they are stable for repeated requests but can't be guessed
// from the text: the key in an audio URL is the only way to fetch it.
type Cache struct {
	mu     sync.Mutex
	secret []byte
	max    int
	ttl    time.Duration
	size   int
	ll     *list.List // front = most recent
	items  map[string]*list.Element
	byPtr  map[*tts.Audio]string
	now    func() time.Time
}

type entry struct {
	key  string
	a    *tts.Audio
	used time.Time
}

func NewCache(maxBytes int, ttl time.Duration) *Cache {
	sec := make([]byte, 32)
	if _, err := rand.Read(sec); err != nil {
		panic(err)
	}
	return &Cache{secret: sec, max: maxBytes, ttl: ttl, ll: list.New(), items: map[string]*list.Element{}, byPtr: map[*tts.Audio]string{}, now: time.Now}
}

// Key is the cache key for one synthesis.
func (c *Cache) Key(provider, voice, model, text string) string {
	m := hmac.New(sha256.New, c.secret)
	for _, p := range []string{provider, voice, model, text} {
		m.Write([]byte(p))
		m.Write([]byte{0}) // unambiguous: "a|b" vs "a" "|b"
	}
	return hex.EncodeToString(m.Sum(nil))
}

func (c *Cache) Get(key string) (*tts.Audio, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*entry)
	if c.now().Sub(e.used) > c.ttl {
		c.remove(el)
		return nil, false
	}
	e.used = c.now()
	c.ll.MoveToFront(el)
	return e.a, true
}

func (c *Cache) Put(key string, a *tts.Audio) {
	if a == nil || len(a.Data) > c.max {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.remove(el)
	}
	c.items[key] = c.ll.PushFront(&entry{key: key, a: a, used: c.now()})
	c.byPtr[a] = key
	c.size += len(a.Data)
	for c.size > c.max || c.expired() {
		c.remove(c.ll.Back())
	}
}

// KeyOf returns the key a is cached under, caching it by its content when
// it isn't (audio that didn't come from Speak).
func (c *Cache) KeyOf(a *tts.Audio) string {
	c.mu.Lock()
	k, ok := c.byPtr[a]
	c.mu.Unlock()
	if ok {
		if _, live := c.Get(k); live {
			return k
		}
	}
	m := hmac.New(sha256.New, c.secret)
	m.Write([]byte("data\x00"))
	m.Write(a.Data)
	k = hex.EncodeToString(m.Sum(nil))
	c.Put(k, a)
	return k
}

func (c *Cache) expired() bool {
	b := c.ll.Back()
	return b != nil && c.now().Sub(b.Value.(*entry).used) > c.ttl
}

func (c *Cache) remove(el *list.Element) {
	if el == nil {
		return
	}
	e := el.Value.(*entry)
	c.ll.Remove(el)
	delete(c.items, e.key)
	if c.byPtr[e.a] == e.key {
		delete(c.byPtr, e.a)
	}
	c.size -= len(e.a.Data)
}
