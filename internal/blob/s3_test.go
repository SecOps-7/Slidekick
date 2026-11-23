package blob_test

import (
	"context"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/SecOps-7/slidekick/internal/blob"
)

// TestS3 runs the Store contract against a real S3-compatible server; it is
// skipped unless SLIDEKICK_TEST_S3_* is set (see s3TestConfig).
func TestS3(t *testing.T) {
	cfg := s3TestConfig(t)
	ctx := context.Background()
	c, err := minio.New(cfg.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""), Secure: !cfg.Insecure, Region: cfg.Region})
	if err != nil {
		t.Fatal(err)
	}
	s, err := blob.NewS3(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.String(), cfg.SecretKey) {
		t.Fatal("String() must not include the secret key")
	}
	testStore(t, s)

	// The prefix is applied to object keys.
	info, err := c.StatObject(ctx, cfg.Bucket, strings.Trim(cfg.Prefix, "/")+"/f_0123456789abcdef0123.txt", minio.StatObjectOptions{})
	if err != nil {
		t.Fatalf("object not stored under prefix: %v", err)
	}
	if info.ContentType != "text/plain" {
		t.Errorf("content type %q, want text/plain", info.ContentType)
	}

	missing := cfg
	missing.Bucket = cfg.Bucket + "-missing-" + strings.Trim(cfg.Prefix, "/")
	if _, err := blob.NewS3(ctx, missing); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing bucket should fail fast, got %v", err)
	}
	bad := cfg
	bad.SecretKey = "wrong-secret-key"
	if _, err := blob.NewS3(ctx, bad); err == nil {
		t.Fatal("bad credentials should fail fast")
	}
}
