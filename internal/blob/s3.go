package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config configures an S3-compatible backend (AWS S3, MinIO, Garage, R2...).
type S3Config struct {
	Endpoint  string // host[:port], no scheme
	Bucket    string
	Region    string // optional
	AccessKey string
	SecretKey string
	Prefix    string // optional key prefix, e.g. "slidekick/"
	Insecure  bool   // plain HTTP instead of HTTPS
}

// S3 stores objects in a bucket.
type S3 struct {
	c      *minio.Client
	bucket string
	prefix string
	desc   string
}

// NewS3 connects to the bucket and checks that it is reachable, so a bad
// configuration fails at startup instead of on the first upload.
func NewS3(ctx context.Context, cfg S3Config) (*S3, error) {
	switch {
	case cfg.Endpoint == "":
		return nil, errors.New("s3: endpoint is required")
	case strings.Contains(cfg.Endpoint, "://"):
		return nil, fmt.Errorf("s3: endpoint %q must be host[:port] without a scheme (use the insecure flag for plain HTTP)", cfg.Endpoint)
	case cfg.Bucket == "":
		return nil, errors.New("s3: bucket is required")
	case cfg.AccessKey == "" || cfg.SecretKey == "":
		return nil, errors.New("s3: access key and secret key are required")
	}
	c, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: !cfg.Insecure,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3: %w", err)
	}
	prefix := strings.Trim(cfg.Prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	scheme := "https"
	if cfg.Insecure {
		scheme = "http"
	}
	s := &S3{c: c, bucket: cfg.Bucket, prefix: prefix,
		desc: fmt.Sprintf("s3 %s://%s/%s/%s", scheme, cfg.Endpoint, cfg.Bucket, prefix)}

	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ok, err := c.BucketExists(cctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("s3: cannot reach bucket %q at %s: %w", cfg.Bucket, cfg.Endpoint, err)
	}
	if !ok {
		return nil, fmt.Errorf("s3: bucket %q does not exist at %s", cfg.Bucket, cfg.Endpoint)
	}
	return s, nil
}

func (s *S3) String() string { return s.desc }

func (s *S3) key(k string) string { return s.prefix + k }

func (s *S3) Put(ctx context.Context, key, contentType string, data []byte) error {
	if ok, err := s.Exists(ctx, key); err != nil {
		return err
	} else if ok {
		return nil
	}
	_, err := s.c.PutObject(ctx, s.bucket, s.key(key), bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	return nil
}

// Open returns minio's *Object, which seeks with ranged GETs, so
// http.ServeContent can answer Range requests without buffering the file.
func (s *S3) Open(ctx context.Context, key string) (io.ReadSeekCloser, Info, error) {
	obj, err := s.c.GetObject(ctx, s.bucket, s.key(key), minio.GetObjectOptions{})
	if err != nil {
		return nil, Info{}, s.mapErr(key, err)
	}
	st, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, Info{}, s.mapErr(key, err)
	}
	return obj, Info{Size: st.Size, ModTime: st.LastModified}, nil
}

func (s *S3) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.c.StatObject(ctx, s.bucket, s.key(key), minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	if err = s.mapErr(key, err); errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return false, err
}

func (s *S3) mapErr(key string, err error) error {
	r := minio.ToErrorResponse(err)
	if r.Code == minio.NoSuchKey || r.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	return fmt.Errorf("s3 %s: %w", key, err)
}
