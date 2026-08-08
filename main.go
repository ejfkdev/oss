// Command oss is an S3-compatible object storage client for the major cloud
// providers (AWS S3, Aliyun OSS, Tencent COS, Huawei OBS, Qiniu Kodo, GCS,
// Cloudflare R2, MinIO, ...). It supports anonymous buckets, URL-style
// targets with query filtering, AK/SK/STS auth, streaming pagination and
// parallel downloads.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/ejfkdev/oss/internal/app"
)

// Version is injected at build time via -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.New(Version).Run(ctx, os.Args); err != nil {
		app.PrintError(err)
		os.Exit(1)
	}
}
