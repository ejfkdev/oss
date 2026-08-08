# oss

[![ci](https://github.com/ejfkdev/oss/actions/workflows/ci.yml/badge.svg)](https://github.com/ejfkdev/oss/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/ejfkdev/oss)](https://github.com/ejfkdev/oss/releases)

**[中文](README.md) | English**

A cross-cloud object storage CLI based on the S3 protocol. One binary for all major providers:
AWS S3, Aliyun OSS, Tencent COS, Huawei OBS, Qiniu Kodo, Google Cloud Storage,
Cloudflare R2, Wasabi, DigitalOcean Spaces, Backblaze B2, MinIO and any other S3-compatible service.

- 🌐 **Anonymous bucket browsing**: just pass a URL to list/download public-read buckets — no credentials needed
- 🔗 **URL as the entry point**: access via `https://...` URLs; provider domains are recognized and
  bucket/region parsed automatically. URL query parameters (`prefix`, `delimiter`, `max-keys`,
  `continuation-token`, `marker`, `start-after`) act as listing filters, similar to
  [aws-s3-bucket-browser](https://github.com/qoomon/aws-s3-bucket-browser)
- 📁 **Path-forwarded buckets**: buckets mounted under a path by a reverse proxy / CDN
  (e.g. `https://files.example.com/mybucket/`) are handled automatically via path-style parsing
- 🎫 **Extra query params passthrough**: non-listing URL parameters (e.g. `?token=abc` auth gateways)
  are injected into every API request (list/download/upload/presign) before SigV4 signing
- 🔑 **Flexible auth**: AK/SK, STS session tokens, AWS profiles (incl. assume-role), anonymous
- 🗂 **Listing cache**: fetched listings are cached locally by default; repeated listing/export
  becomes instant (hundreds of times faster for `-d`-style full-scan cases)
- 📤 **Export file lists**: export filtered listings to `txt / csv / xlsx / yaml / markdown` (`--export`)
- ⬇️ **Filtered batch download**: `cp -r` downloads everything matching
  `--include/--exclude/--prefix`, preserving the key directory structure
- 🎨 **Terminal-friendly**: colored output on interactive terminals, plain text when piped
  (`--color auto|always|never`)
- 🌍 **Bilingual help**: the system language is auto-detected — Chinese help in Chinese
  environments, English otherwise (override with `OSS_LANG=zh|en`)
- 📄 **Huge-bucket friendly**: streaming pagination (constant memory), NDJSON streaming output,
  bounded concurrent downloads — millions of objects without memory pressure
- 🚀 **Parallel transfers**: multi-file concurrency + per-file multipart concurrency, progress bars,
  skip-existing, atomic `.part` writes
- 🛠 **Network control**: `-x` proxy, `-H` custom headers (UA / Cookie …), `-k` skip TLS verification

## Installation

**Option 1: Homebrew** (macOS / Linux, recommended)

```bash
brew install ejfkdev/tap/oss
```

**Option 2: go install** (requires Go 1.24+)

```bash
go install github.com/ejfkdev/oss@latest
```

**Option 3: prebuilt binaries**

Download the archive for your platform from [GitHub Releases](https://github.com/ejfkdev/oss/releases)
(`linux/darwin/windows × amd64/arm64`) and extract. Release binaries are stripped;
Linux/Windows builds are UPX-compressed (macOS builds are not: UPX-packed Mach-O binaries are
killed by the kernel). Every release ships with a `SHA256` checksum file.

**Option 4: build from source** (requires Go 1.24+):

```bash
git clone https://github.com/ejfkdev/oss && cd oss
make build      # produces ./oss
make install    # installs into $GOPATH/bin
make release    # cross-compiles all platforms into dist/
```

## Quick start

```bash
# Browse a public bucket anonymously (no credentials)
oss ls https://noaa-nwm-pds.s3.amazonaws.com/?delimiter=/
oss ls "https://noaa-nwm-pds.s3.amazonaws.com/?prefix=nwm.20250101/&delimiter=/&max-keys=20"
oss cp -r https://noaa-nwm-pds.s3.amazonaws.com/nwm.20250101/ ./nwm/ --jobs 32

# Aliyun OSS
oss ls oss://mybucket/logs/ --ak <AK> --sk <SK> --region cn-hangzhou
export OSS_ACCESS_KEY_ID=xxx OSS_SECRET_ACCESS_KEY=yyy   # or use env vars
oss ls mybucket --provider aliyun                         # bare bucket name

# Tencent COS / Huawei OBS / Qiniu
oss ls s3://bucket-1250000000 --provider tencent --region ap-guangzhou
oss ls https://bucket.obs.cn-north-4.myhuaweicloud.com/prefix/
oss ls s3://mybucket --provider qiniu --region cn-east-1

# MinIO / self-hosted S3
oss ls s3://mybucket -e http://127.0.0.1:9000 --ak minioadmin --sk minioadmin
oss ls http://127.0.0.1:9000/mybucket/prefix/             # IP/port auto-detected as path-style

# Proxy, custom UA / Cookie
oss ls s3://mybucket -x http://127.0.0.1:7890 \
    -H "User-Agent: my-agent/1.0" -H "Cookie: session=abc"

# Path-forwarded bucket (reverse proxy mounting a bucket under a path)
oss ls "https://files.example.com/mybucket/?prefix=2026/&max-keys=10"
oss cp "https://files.example.com/mybucket/<key>" ./

# Access with extra parameters (token etc. is attached to every request)
oss ls "http://gateway.example.com/bucket?token=abc"
oss cp "http://gateway.example.com/bucket/file.bin?token=abc" ./
```

## Target syntax

| Form | Example |
|---|---|
| scheme | `s3://bucket/prefix`, `oss://`, `cos://`, `obs://` |
| bare bucket | `mybucket/prefix` (with `--provider` / `-e`) |
| HTTP URL | `https://bucket.s3.us-east-1.amazonaws.com/prefix?prefix=logs/` |

Provider domains recognized automatically (endpoint / region / bucket are parsed):

| Provider | Domain example |
|---|---|
| AWS | `bucket.s3.region.amazonaws.com`, `s3.region.amazonaws.com/bucket` |
| Aliyun | `bucket.oss-cn-hangzhou.aliyuncs.com` (incl. accelerate) |
| Tencent | `bucket-appid.cos.ap-guangzhou.myqcloud.com` |
| Huawei | `bucket.obs.cn-north-4.myhuaweicloud.com` |
| Qiniu | `bucket.s3-cn-east-1.qiniucs.com` |
| Cloudflare R2 | `bucket.<account_id>.r2.cloudflarestorage.com` |
| GCS | `storage.googleapis.com/bucket` (HMAC keys) |

Any other host (MinIO, Ceph, path-forwarded buckets, CNAME…) is parsed as **path-style**:
the first path segment is the bucket, the rest is the key, e.g.
`https://files.example.com/mybucket/sub/key.jpg` → bucket `mybucket`, key `sub/key.jpg`.
If the host points directly at a bucket (CNAME), use `--bucket NAME` explicitly.
With no target at all, `oss ls` lists all buckets.

**URL query parameters fall into two categories**:

- Listing API parameters (`prefix` `delimiter` `max-keys` `continuation-token` `marker`
  `start-after` `encoding-type` `list-type`) → interpreted as listing filters
- Everything else (e.g. `token=abc`) → treated as extra parameters, **passed through to every
  request** against that target (ListObjects / GetObject / PutObject / Presign …). Useful for
  auth-gateway setups; parameters are injected before SigV4 signing and take part in the
  signature, so they work together with AK/SK credentials

## Authentication

Resolved in the following order, first match wins:

1. `--ak` / `--sk` / `--token` (STS session token)
2. Env vars `OSS_ACCESS_KEY_ID` / `OSS_SECRET_ACCESS_KEY` / `OSS_SESSION_TOKEN`
3. Env vars `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_SESSION_TOKEN`
4. `--profile NAME` or `~/.aws/credentials|config` (STS profiles such as assume-role supported)
5. Nothing → **anonymous access** (force with `--anonymous`)

## Commands

### `oss ls` — list buckets/objects

```bash
oss ls                                      # list all buckets
oss ls s3://b/dir/ --limit 50               # folder view (delimiter=/), first 50 entries
oss ls s3://b/dir/ --limit 50               # run again: auto-resume from the breakpoint
oss ls s3://b/dir/ --reset                  # clear the cache and relist from scratch
oss ls s3://b/dir/ --next-token <t>         # explicit token (overrides the cache)
oss ls s3://b/dir/ -d                       # directories only (PRE = common prefix)
oss ls s3://b/dir/ -f --include "*.gz"      # files only, glob filter
oss ls s3://b --all                         # stream everything (constant memory)
oss ls s3://b -r                            # flat listing of all keys
oss ls s3://b --all -j > list.ndjson        # NDJSON streaming output, pipe-friendly
oss ls s3://b --export list.xlsx            # export the file list to Excel
```

`ls` is read-only (list/filter/export); **use `cp` to download/upload** (see below).

Flag groups (see `oss ls -h` for details):

- **Filter**: `--prefix`, `--delimiter`, `-r/--recursive`, `-d/--dirs`, `-f/--files`, `--include`, `--exclude`
- **Paging & cache**: `-n/--limit`, `--all`, `--page-size`, `--next-token`, `--reset`, `--no-cache`, `--start-after`
- **Output**: `-j/--json`, `--bytes`, `--color auto|always|never`
- **Export**: `--export <file>` (format by extension: `.txt .csv .xlsx .yaml/.yml .md`)

**Output & colors**: `PRE` marks a common prefix (directory). On interactive terminals output is
beautified automatically:

- Directories: bold blue (both the `PRE` marker and the name)
- File sizes colored by magnitude: <1 MiB green, <1 GiB yellow, ≥1 GiB red
- Dates dimmed; `stat` labels cyan and display-width aligned (double-width aware for CJK)
- Success messages green `✓`, truncation/resume hints yellow, errors red
- All messages follow the system language (Chinese/English)

When output is piped/redirected, **plain text** is emitted without any ANSI escape codes
(safe for scripts). `--color always|never` forces it on/off (available on all subcommands).

**Export** (`--export`): exports the full filtered listing (ignores the `-n` display limit), with
type/key/size/last_modified/etag/storage_class fields; format chosen by file extension.
The first export performs a full listing and fills the cache; later exports/listings with the
same parameters hit the cache instantly.

**Paging & listing cache** (on by default):

- When a listing is truncated by `--limit`, rerunning the same command auto-resumes from the
  breakpoint — no manual token handling; `--next-token` can still override it (bypasses the cache)
- **Fetched listings are cached locally** (`~/Library/Caches/ejfkdev/oss/ls-cache.json` on macOS,
  `~/.cache/ejfkdev/oss/` on Linux); repeated listings read from the cache without touching the
  server. This dramatically speeds up `-d`-style cases that must scan many files to find
  directories (measured 17s → 0.03s)
- Cache entries are isolated by an `endpoint+bucket+prefix+delimiter` fingerprint, expire after
  24 hours, and hold up to 50k entries per listing (beyond that it degrades to token-based
  resume — still correct)
- Once a listing runs to completion, the next run replays the full snapshot; `--reset` clears the
  cache and refetches; `--no-cache` disables the cache entirely (live results, manual
  `--next-token` paging); `--all` streams directly and never uses the cache

### `oss cat` — print object content

```bash
oss cat s3://b/config.yaml
oss cat s3://b/big.bin --range 0-1023       # read a byte range only
```

### `oss stat` — show metadata

```bash
oss stat s3://b/dir/file.tar.gz             # object: size/etag/type/custom metadata
oss stat s3://b                             # bucket reachability
```

### `oss cp` — download / upload / cross-bucket copy

```bash
oss cp s3://b/path/file.tar.gz ./           # download one file (multipart concurrency)
oss cp s3://b/path/file.tar.gz -            # download to stdout
oss cp -r s3://b/logs/ ./logs --include "*.gz"   # filtered batch download
oss cp -r s3://b/dataset/ ./data --jobs 32 --parallel 8 \
        --exclude "*.tmp" --skip-existing   # recursive download with glob filters
oss cp ./dist s3://b/release/ -r            # upload a directory recursively
oss cp ./a.bin s3://b/b.bin                 # upload one file (auto multipart)
oss cp s3://b1/k s3://b2/k                  # server-side copy (same endpoint)
```

Common flags: `-r`, `--jobs` (parallel files, default 16), `--parallel` (parts per file, default 5),
`--include` / `--exclude` (globs, repeatable), `--skip-existing`, `--no-progress`.

Directory layout: with `-r`, slashes in keys become local directories level by level; when the
source ends with `/`, its contents are placed directly into the destination
(e.g. `s3://b/logs/` → `./logs/<files>`).

### `oss presign` — pre-signed URLs

```bash
oss presign s3://b/key --expires 1h         # GET download link
oss presign s3://b/key --method PUT         # upload link
```

## Global connection flags

Supported by every subcommand:

| Flag | Description |
|---|---|
| `--ak` / `--sk` / `--token` | static credentials / STS session token |
| `--profile` | AWS shared config profile (assume-role supported) |
| `--anonymous` | force anonymous access |
| `--provider` | `aws\|aliyun\|tencent\|huawei\|qiniu\|gcs\|r2\|wasabi\|spaces\|b2\|minio` |
| `-e/--endpoint` | custom endpoint (overrides the provider default) |
| `--region` | region (overrides the URL-derived one) |
| `--path-style` | force path-style addressing |
| `--bucket` | explicit bucket name when the URL is ambiguous |
| `-x/--proxy` | HTTP proxy, e.g. `http://127.0.0.1:7890` |
| `-H/--header` | extra HTTP headers, repeatable: `-H "User-Agent: …" -H "Cookie: …"` |
| `-k/--insecure` | skip TLS certificate verification |
| `--timeout` | per-request timeout, e.g. `30s` (default: none, suits large streaming downloads) |
| `--color` | color output `auto\|always\|never` (auto = interactive terminals only) |

## Huge-bucket design

- **Streaming pagination**: pages are fetched and printed entry by entry; with `--all` memory
  stays constant at one page (`--page-size`) — millions of keys are never buffered
- **NDJSON**: `-j` emits one JSON per line (sonic-encoded), streamed into pipes, interruptible anytime
- **Bounded concurrent downloads**: the listing stream feeds a fixed-size worker pool (`--jobs`);
  when workers are saturated the lister is back-pressured, so the task queue never grows unbounded
- **Multipart concurrency**: each large file is downloaded with `--parallel` concurrent parts
- **Atomic writes**: files are written to `*.part` then renamed — an interruption never leaves
  half-written files
- **v2 → v1 fallback**: when the server lacks ListObjectsV2, marker-based pagination is used automatically

## Command reference

Complete parameters for every subcommand (also available via `oss <command> -h`).

### `oss ls` — list buckets/objects

| Flag | Default | Description |
|---|---|---|
| `--prefix <p>` | | key prefix filter (stacks with URL path / `?prefix=`) |
| `--delimiter <d>` | `/` | delimiter for folder view; empty string = flat |
| `-r, --recursive` | | flat listing of every key (no folders) |
| `-d, --dirs` | | list directories only |
| `-f, --files` | | list files (objects) only |
| `--include <glob>` | | glob include filter, repeatable (matches relative path or base name) |
| `--exclude <glob>` | | glob exclude filter, repeatable |
| `-n, --limit <N>` | `1000` | max entries to display; next run auto-resumes |
| `-a, --all` | | list everything (streaming, constant memory, no cache) |
| `--page-size <N>` | `1000` | entries per ListObjects request |
| `--next-token <t>` | | explicit continuation token (overrides the cache) |
| `--reset` | | clear this listing's cache and refetch |
| `--no-cache` | | bypass the listing cache (live results; manual `--next-token` paging) |
| `--start-after <k>` | | start listing after this key |
| `-j, --json` | | NDJSON output (one entry per line, streaming) |
| `--bytes` | | raw byte sizes (default: human-readable) |
| `--export <file>` | | export the filtered list; format by extension `.txt .csv .xlsx .yaml .md` |

### `oss cat` — print object content

| Flag | Default | Description |
|---|---|---|
| `--range <r>` | | byte range: `0-1023`, `bytes=0-1023`, `100-` (open end) |

### `oss stat` — show metadata

No dedicated flags (object → size/etag/type/custom metadata; bucket → reachability and connection info).

### `oss cp` — download / upload / cross-bucket copy

| Flag | Default | Description |
|---|---|---|
| `-r, --recursive` | | copy recursively by prefix/directory |
| `--include <glob>` | | glob include filter, repeatable |
| `--exclude <glob>` | | glob exclude filter, repeatable (e.g. `*.tmp`) |
| `--skip-existing` | | skip files that already exist at the destination |
| `--jobs <N>` | `16` | parallel file transfers |
| `--parallel <N>` | `5` | parallel parts per file (multipart) |
| `--no-progress` | | disable progress output |

### `oss presign` — pre-signed URLs

| Flag | Default | Description |
|---|---|---|
| `--expires <d>` | `15m` | URL validity, e.g. `15m`, `1h` |
| `--method <m>` | `GET` | HTTP method to sign: `GET` (download) \| `PUT` (upload) |

### Common connection flags (all subcommands)

| Flag | Description |
|---|---|
| `--ak <ID>` | AccessKey ID (env `OSS_ACCESS_KEY_ID` / `AWS_ACCESS_KEY_ID`) |
| `--sk <SECRET>` | AccessKey Secret (env `OSS_SECRET_ACCESS_KEY` / `AWS_SECRET_ACCESS_KEY`) |
| `--token <T>` | STS session token (env `OSS_SESSION_TOKEN` / `AWS_SESSION_TOKEN`) |
| `--profile <NAME>` | AWS shared config profile (`~/.aws/config`, assume-role supported) |
| `--anonymous` | force anonymous access |
| `--provider <P>` | storage provider: `aws aliyun tencent huawei qiniu gcs r2 wasabi spaces b2 minio` |
| `-e, --endpoint <URL>` | custom endpoint (overrides the provider default) |
| `--region <R>` | region (overrides the URL-derived one) |
| `--path-style` | force path-style addressing (`http://host/bucket/key`) |
| `--bucket <NAME>` | explicit bucket name (when the URL is ambiguous) |
| `-x, --proxy <URL>` | HTTP proxy, e.g. `http://127.0.0.1:7890` |
| `-H, --header "K: V"` | extra HTTP header, repeatable (User-Agent, Cookie, ...) |
| `-k, --insecure` | skip TLS certificate verification |
| `--timeout <d>` | per-request timeout, e.g. `30s` (0 = none) |
| `--color <WHEN>` | color output: `auto` (default) \| `always` \| `never` |

## Development

```bash
make test       # unit tests (URL parsing / providers / headers)
make fmt
make tidy
```

Main dependencies: [aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2),
[sonic](https://github.com/bytedance/sonic), [urfave/cli v3](https://github.com/urfave/cli),
[progressbar/v3](https://github.com/schollz/progressbar),
[go-humanize](https://github.com/dustin/go-humanize), [errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup).
