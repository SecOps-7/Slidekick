package testenv

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/SecOps-7/slidekick/internal/blob"
)

// S3Config returns the S3 integration-test settings, skipping the test unless
// SLIDEKICK_TEST_S3_ENDPOINT is set. It also needs SLIDEKICK_TEST_S3_ACCESS_KEY
// and SLIDEKICK_TEST_S3_SECRET_KEY; SLIDEKICK_TEST_S3_BUCKET (default
// "slidekick-test") is created if missing, SLIDEKICK_TEST_S3_REGION is
// optional and SLIDEKICK_TEST_S3_INSECURE=1 uses plain HTTP (local MinIO).
// Each call gets a fresh random key prefix so runs don't see each other.
func S3Config(t testing.TB) blob.S3Config {
	t.Helper()
	cfg := blob.S3Config{
		Endpoint:  os.Getenv("SLIDEKICK_TEST_S3_ENDPOINT"),
		Bucket:    os.Getenv("SLIDEKICK_TEST_S3_BUCKET"),
		Region:    os.Getenv("SLIDEKICK_TEST_S3_REGION"),
		AccessKey: os.Getenv("SLIDEKICK_TEST_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("SLIDEKICK_TEST_S3_SECRET_KEY"),
		Insecure:  os.Getenv("SLIDEKICK_TEST_S3_INSECURE") == "1",
	}
	if cfg.Endpoint == "" {
		t.Skip("SLIDEKICK_TEST_S3_ENDPOINT not set")
	}
	if cfg.Bucket == "" {
		cfg.Bucket = "slidekick-test"
	}
	ctx := context.Background()
	c, err := minio.New(cfg.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""), Secure: !cfg.Insecure, Region: cfg.Region})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := c.BucketExists(ctx, cfg.Bucket); err != nil {
		t.Fatal(err)
	} else if !ok {
		if err := c.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			if ok, _ := c.BucketExists(ctx, cfg.Bucket); !ok { // another package may have raced us
				t.Fatal(err)
			}
		}
	}
	rb := make([]byte, 6)
	rand.Read(rb)
	cfg.Prefix = "/test-" + hex.EncodeToString(rb) + "/"
	return cfg
}
