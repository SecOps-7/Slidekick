package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SecOps-7/slidekick/internal/blob"
	"github.com/SecOps-7/slidekick/internal/store"
)

// MaxAssetBytes caps one upload.
const MaxAssetBytes = 15 << 20

// AllowedMIME maps uploadable types to the extension they are stored with.
var AllowedMIME = map[string]string{
	"image/png":     ".png",
	"image/jpeg":    ".jpg",
	"image/gif":     ".gif",
	"image/webp":    ".webp",
	"image/avif":    ".avif",
	"image/svg+xml": ".svg",
	"video/mp4":     ".mp4",
	"video/webm":    ".webm",
}

var assetIDRe = regexp.MustCompile(`^f_[0-9a-f]{20}$`)

// UploadAsset stores bytes content-addressed in Storage() and returns the
// asset. Identical bytes always get the same id.
func (s *Service) UploadAsset(ctx context.Context, name, mime string, data []byte) (*store.Asset, error) {
	if s.Storage() == nil {
		return nil, errors.New("asset storage is not configured")
	}
	mime = strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	ext, ok := AllowedMIME[mime]
	if !ok {
		return nil, fmt.Errorf("unsupported type %q (allowed: png, jpeg, gif, webp, avif, svg, mp4, webm)", mime)
	}
	if len(data) == 0 {
		return nil, errors.New("file is empty")
	}
	if len(data) > MaxAssetBytes {
		return nil, fmt.Errorf("file is %d bytes; the limit is %d", len(data), MaxAssetBytes)
	}
	if err := checkContent(mime, data); err != nil {
		return nil, err
	}
	return s.saveFile(ctx, name, mime, ext, data)
}

// generatedMIME are types the server itself produces (exports); users can't upload them.
var generatedMIME = map[string]string{"application/pdf": ".pdf", "application/zip": ".zip"}

// SaveGenerated stores a server-made file (PDF or zip export) as an asset.
func (s *Service) SaveGenerated(ctx context.Context, name, mime string, data []byte) (*store.Asset, error) {
	ext, ok := generatedMIME[mime]
	if !ok {
		return nil, fmt.Errorf("not a generated type: %s", mime)
	}
	return s.saveFile(ctx, name, mime, ext, data)
}

// Storage returns where file bytes live: Blobs if set, else a disk store
// under FilesDir, else nil (uploads disabled).
func (s *Service) Storage() blob.Store {
	if s.Blobs != nil {
		return s.Blobs
	}
	if s.FilesDir != "" {
		return blob.NewDisk(s.FilesDir)
	}
	return nil
}

func (s *Service) saveFile(ctx context.Context, name, mime, ext string, data []byte) (*store.Asset, error) {
	bs := s.Storage()
	if bs == nil {
		return nil, errors.New("asset storage is not configured")
	}
	sum := sha256.Sum256(data)
	hexsum := hex.EncodeToString(sum[:])
	a := &store.Asset{ID: "f_" + hexsum[:20], Name: cleanName(name), MIME: mime, Ext: ext, Size: int64(len(data)), SHA256: hexsum}

	if err := bs.Put(ctx, a.ID+a.Ext, mime, data); err != nil {
		return nil, err
	}
	if err := s.store.PutAsset(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// OpenAsset resolves "f_xxx" or "f_xxx.ext" to its metadata and a seekable
// reader over its bytes. The caller closes the reader.
func (s *Service) OpenAsset(ctx context.Context, name string) (*store.Asset, io.ReadSeekCloser, error) {
	id := strings.TrimSuffix(name, filepath.Ext(name))
	if !assetIDRe.MatchString(id) {
		return nil, nil, store.ErrNotFound
	}
	bs := s.Storage()
	if bs == nil {
		return nil, nil, store.ErrNotFound
	}
	a, err := s.store.GetAsset(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	r, _, err := bs.Open(ctx, a.ID+a.Ext)
	if errors.Is(err, blob.ErrNotFound) {
		return nil, nil, store.ErrNotFound
	} else if err != nil {
		return nil, nil, err
	}
	return a, r, nil
}

// ReadAsset returns an asset's bytes, for export inlining.
func (s *Service) ReadAsset(ctx context.Context, name string) (*store.Asset, []byte, error) {
	a, r, err := s.OpenAsset(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	return a, b, err
}

// checkContent makes sure the bytes really are the declared kind of file.
func checkContent(mime string, data []byte) error {
	if mime == "image/svg+xml" {
		head := bytes.ToLower(data[:min(len(data), 1024)])
		if !bytes.Contains(head, []byte("<svg")) {
			return errors.New("file does not look like an SVG")
		}
		return nil
	}
	sniffed := http.DetectContentType(data)
	switch {
	case mime == "image/avif":
		if !bytes.Contains(data[:min(len(data), 64)], []byte("ftypavif")) {
			return errors.New("file does not look like an AVIF image")
		}
	case strings.HasPrefix(mime, "video/"):
		if !strings.HasPrefix(sniffed, "video/") && sniffed != "application/octet-stream" {
			return fmt.Errorf("file looks like %s, not %s", sniffed, mime)
		}
	case sniffed != mime:
		return fmt.Errorf("file looks like %s, not %s", sniffed, mime)
	}
	return nil
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._ -]+`)

func cleanName(n string) string {
	n = unsafeName.ReplaceAllString(filepath.Base(n), "_")
	if len(n) > 120 {
		n = n[:120]
	}
	if n == "" || n == "." {
		n = "file"
	}
	return n
}
