package blob

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Disk stores objects as files in one directory (the default backend).
type Disk struct{ Dir string }

// NewDisk returns a Store rooted at dir; the directory is created on first Put.
func NewDisk(dir string) *Disk { return &Disk{Dir: dir} }

func (d *Disk) String() string { return "disk " + d.Dir }

func (d *Disk) path(key string) (string, error) {
	if key == "" || key != filepath.Base(key) || strings.HasPrefix(key, ".") {
		return "", errors.New("blob: invalid key")
	}
	return filepath.Join(d.Dir, key), nil
}

func (d *Disk) Put(_ context.Context, key, _ string, data []byte) error {
	dst, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.Dir, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	tmp, err := os.CreateTemp(d.Dir, ".upload-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func (d *Disk) Open(_ context.Context, key string) (io.ReadSeekCloser, Info, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, Info{}, ErrNotFound
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, Info{}, ErrNotFound
	} else if err != nil {
		return nil, Info{}, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, Info{}, err
	}
	return f, Info{Size: st.Size(), ModTime: st.ModTime()}, nil
}

func (d *Disk) Exists(_ context.Context, key string) (bool, error) {
	p, err := d.path(key)
	if err != nil {
		return false, nil
	}
	_, err = os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
