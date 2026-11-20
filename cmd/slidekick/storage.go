package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/SecOps-7/slidekick/internal/blob"
)

// openBlobs picks where uploaded and generated files are stored: an
// S3-compatible bucket when SLIDEKICK_S3_BUCKET is set, else <data>/files.
// The secret key is never logged.
func openBlobs(ctx context.Context, data string, log *slog.Logger) (blob.Store, error) {
	var bs blob.Store
	if bucket := os.Getenv("SLIDEKICK_S3_BUCKET"); bucket != "" {
		s3, err := blob.NewS3(ctx, blob.S3Config{
			Endpoint:  os.Getenv("SLIDEKICK_S3_ENDPOINT"),
			Bucket:    bucket,
			Region:    os.Getenv("SLIDEKICK_S3_REGION"),
			AccessKey: os.Getenv("SLIDEKICK_S3_ACCESS_KEY"),
			SecretKey: os.Getenv("SLIDEKICK_S3_SECRET_KEY"),
			Prefix:    os.Getenv("SLIDEKICK_S3_PREFIX"),
			Insecure:  os.Getenv("SLIDEKICK_S3_INSECURE") == "1",
		})
		if err != nil {
			return nil, err
		}
		bs = s3
	} else {
		bs = blob.NewDisk(filepath.Join(data, "files"))
	}
	log.Info("file storage", "backend", bs.String())
	return bs, nil
}
