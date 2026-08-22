// Command oss is an S3-compatible object storage client for the major cloud
// providers (AWS S3, Aliyun OSS, Tencent COS, Huawei OBS, Baidu BOS, Kingsoft
// KS3, UCloud US3, JD Cloud OSS, GCS, Cloudflare R2, MinIO, ...). It supports
// anonymous buckets, URL-style targets with query filtering, AK/SK/STS auth,
// streaming pagination and parallel downloads — through a CLI, an HTTP REST
// service (oss serve) and an MCP tool server (oss mcp).
package main

import (
	"os"
	"runtime/debug"
	"strings"

	"github.com/ejfkdev/oss/internal/app"
)

// Version is injected at build time via -ldflags "-X main.Version=...".
// When unset (e.g. `go install`), the module version from the binary's
// build info is used instead. The leading "v" (present in git tags) is
// normalized away; display code adds it back.
var Version = ""

func version() string {
	if Version != "" {
		return strings.TrimPrefix(Version, "v")
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "dev"
}

func main() {
	os.Exit(app.Run(version(), os.Args[1:]))
}