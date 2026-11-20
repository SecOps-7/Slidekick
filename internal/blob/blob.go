// Package blob stores the bytes of uploaded and generated files. Metadata
// stays in SQLite; a Store only maps a key ("f_<hash>.<ext>") to bytes.
// Keys are content-addressed, so a Put of an existing key is a no-op.
package blob

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned by Open when the key has no object.
var ErrNotFound = errors.New("blob not found")

// Info describes a stored object.
type Info struct {
	Size    int64
	ModTime time.Time // zero if the backend doesn't know
}

// Store is a flat key/value store for file bytes.
type Store interface {
	// Put stores data under key unless the key already exists.
	Put(ctx context.Context, key, contentType string, data []byte) error
	// Open returns a seekable reader over the object. The caller closes it.
	Open(ctx context.Context, key string) (io.ReadSeekCloser, Info, error)
	// Exists reports whether key has an object.
	Exists(ctx context.Context, key string) (bool, error)
	// String names the backend for logs; it never includes credentials.
	String() string
}

// ReadAll reads a whole object.
func ReadAll(ctx context.Context, s Store, key string) ([]byte, error) {
	r, _, err := s.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
