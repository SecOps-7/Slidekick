// Package secret encrypts small secrets at rest (SSO client secrets, AI
// provider keys) with AES-256-GCM. The key comes from SLIDEKICK_SECRET_KEY or,
// when unset, from a key file created once in the data directory (mode 600).
// Each value is bound to its purpose (the "additional data"), so a ciphertext
// copied from one record to another fails to decrypt.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const prefix = "v1:"

type Box struct{ aead cipher.AEAD }

// New builds a box from a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secret key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// ParseKey accepts 64 hex characters or standard/URL base64 of 32 bytes.
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, errors.New("SLIDEKICK_SECRET_KEY must be 32 bytes as 64 hex characters or base64 (try `openssl rand -hex 32`)")
}

// Load returns the box for envKey if set, else for <dataDir>/secret.key,
// creating that file with a random key on first use.
func Load(envKey, dataDir string) (*Box, string, error) {
	if envKey != "" {
		k, err := ParseKey(envKey)
		if err != nil {
			return nil, "", err
		}
		b, err := New(k)
		return b, "SLIDEKICK_SECRET_KEY", err
	}
	path := filepath.Join(dataDir, "secret.key")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		k := make([]byte, 32)
		if _, err := rand.Read(k); err != nil {
			return nil, "", err
		}
		if err := os.MkdirAll(dataDir, 0o700); err != nil {
			return nil, "", err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, "", err
		}
		_, werr := f.WriteString(hex.EncodeToString(k) + "\n")
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return nil, "", werr
		}
		raw = []byte(hex.EncodeToString(k))
	} else if err != nil {
		return nil, "", err
	}
	k, err := ParseKey(string(raw))
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	b, err := New(k)
	return b, path, err
}

// Seal encrypts plaintext for a purpose (e.g. "sso:entra" or "ai:openai").
func (b *Box) Seal(plaintext, purpose string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := b.aead.Seal(nonce, nonce, []byte(plaintext), []byte(purpose))
	return prefix + base64.RawStdEncoding.EncodeToString(ct), nil
}

// Open decrypts a value sealed for the same purpose.
func (b *Box) Open(sealed, purpose string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	if !strings.HasPrefix(sealed, prefix) {
		return "", errors.New("secret: unknown format")
	}
	raw, err := base64.RawStdEncoding.DecodeString(sealed[len(prefix):])
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", errors.New("secret: corrupt value")
	}
	pt, err := b.aead.Open(nil, raw[:b.aead.NonceSize()], raw[b.aead.NonceSize():], []byte(purpose))
	if err != nil {
		return "", errors.New("secret: can't decrypt (wrong SLIDEKICK_SECRET_KEY or value moved between records)")
	}
	return string(pt), nil
}
