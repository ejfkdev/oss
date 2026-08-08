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
	"runtime/debug"
	"syscall"

	"github.com/ejfkdev/oss/internal/app"
)

// Version is injected at build time via -ldflags "-X main.Version=...".
// When unset (e.g. `go install`), the module version from the binary's
// build info is used instead.
var Version = ""

func version() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.New(version()).Run(ctx, os.Args); err != nil {
		app.PrintError(err)
		os.Exit(1)
	}
}
