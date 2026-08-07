package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/bytedance/sonic"
	"github.com/urfave/cli/v3"

	"oss/internal/s3x"
)

func lsCmd() *cli.Command {
	flags := append([]cli.Flag{
		// ---- filter ----
		&cli.StringFlag{Name: "prefix", Usage: T("前缀过滤（与 URL 路径、?prefix= 叠加生效）", "key prefix filter (stacks with URL path / ?prefix=)")},
		&cli.StringFlag{Name: "delimiter", Value: "/", Usage: T("目录分隔符，默认 /（目录视图）；设为空串则平铺", "delimiter, default / (folder view); empty string = flat")},
		&cli.BoolFlag{Name: "recursive", Aliases: []string{"r"}, Usage: T("平铺列出所有对象（不区分目录）", "flat listing of every key (no folders)")},
		&cli.BoolFlag{Name: "dirs", Aliases: []string{"d"}, Usage: T("只列目录", "list directories only")},
		&cli.BoolFlag{Name: "files", Aliases: []string{"f"}, Usage: T("只列文件（对象）", "list files (objects) only")},
		&cli.StringSliceFlag{Name: "include", Usage: T("glob 包含过滤，可重复（匹配相对路径或文件名，如 *.gz）", "glob include filter, repeatable (matches relative path or base name, e.g. *.gz)")},
		&cli.StringSliceFlag{Name: "exclude", Usage: T("glob 排除过滤，可重复", "glob exclude filter, repeatable")},
		// ---- paging & cache ----
		&cli.Int64Flag{Name: "limit", Aliases: []string{"n"}, Value: 1000, Usage: T("最多显示条数，默认 1000；下次运行自动从断点继续", "max entries to display, default 1000; next run auto-resumes")},
		&cli.BoolFlag{Name: "all", Aliases: []string{"a"}, Usage: T("列出全部（流式输出，内存恒定，不使用缓存）", "list everything (streaming, constant memory, no cache)")},
		&cli.Int64Flag{Name: "page-size", Value: 1000, Usage: T("每次 ListObjects 请求的条数", "entries per ListObjects request")},
		&cli.StringFlag{Name: "next-token", Usage: T("显式指定续传 token（不使用缓存）", "explicit continuation token (bypasses cache)")},
		&cli.BoolFlag{Name: "reset", Usage: T("清除该列举的缓存，重新从服务端获取", "clear this listing's cache and refetch")},
		&cli.BoolFlag{Name: "no-cache", Usage: T("不读写列举缓存（结果实时，翻页需手动 --next-token）", "bypass the listing cache (live results; page manually with --next-token)")},
		&cli.StringFlag{Name: "start-after", Usage: T("从指定 key 之后开始列举", "start listing after this key")},
		// ---- output ----
		&cli.BoolFlag{Name: "json", Aliases: []string{"j"}, Usage: T("NDJSON 输出（每行一条，流式）", "NDJSON output (one entry per line, streaming)")},
		&cli.BoolFlag{Name: "bytes", Usage: T("大小显示为字节数（默认人类可读）", "raw byte sizes (default: human-readable)")},
		// ---- export ----
		&cli.StringFlag{Name: "export", Usage: T("导出筛选后的文件列表到文件，格式按扩展名: .txt .csv .xlsx .yaml .md", "export the filtered list to a file; format by extension: .txt .csv .xlsx .yaml .md")},
	}, connFlags()...)
	return &cli.Command{
		Name:    "ls",
		Aliases: []string{"list"},
		Usage:   T("列出桶或对象", "list buckets or objects"),
		UsageText: T(`oss ls [目标]

ls 只负责"看"：列举、过滤、分页、导出列表。下载/上传请使用 cp 命令。

目标写法:
   s3://bucket/prefix/             scheme 写法（也支持 oss:// cos:// obs://）
   mybucket/prefix/                裸桶名（配合 --provider 或环境变量）
   https://bucket.s3.us-east-1.amazonaws.com/logs/?prefix=2026/&delimiter=/
                                   完整 URL；?prefix/delimiter/max-keys 等查询参数即过滤条件
   https://host/bucket/prefix/     目录转发桶 / MinIO（自动 path-style）

示例:
   oss ls                                          列出所有桶
   oss ls s3://mybucket/logs/ -n 50                列前 50 条（再次运行自动继续）
   oss ls s3://mybucket/logs/ -d                   只列目录（PRE=目录/公共前缀）
   oss ls s3://mybucket/logs/ -f --include "*.gz"  只列文件并过滤
   oss ls s3://mybucket -r -a -j > list.ndjson     全量平铺导出 NDJSON
   oss ls s3://mybucket --export list.xlsx         导出文件列表为 Excel

输出说明:
   PRE  = 公共前缀（目录）；交互终端下自动彩色高亮，管道输出为纯文本。

分页与缓存:
   默认只显示 --limit 条，再次运行同一命令自动从断点继续。
   已获取的列表内容默认缓存在本地（24 小时有效），重复列举直接读缓存。
   --reset 清除缓存重新获取，--no-cache 禁用缓存，--all 全量流式列举。

导出:
   --export 按 --prefix/-d/-f/--include/--exclude 筛选后导出完整列表
   （不受 --limit 限制），格式由扩展名决定: txt csv xlsx yaml/yml md。

下载文件请使用:
   oss cp -r s3://mybucket/logs/ ./logs --include "*.gz"   按条件批量下载`,
			`oss ls [target]

ls is read-only: list, filter, page, export the list. Use cp to download/upload.

TARGETS:
   s3://bucket/prefix/             scheme style (also oss:// cos:// obs://)
   mybucket/prefix/                bare bucket (with --provider or env vars)
   https://bucket.s3.us-east-1.amazonaws.com/logs/?prefix=2026/&delimiter=/
                                   full URL; ?prefix/delimiter/max-keys act as filters
   https://host/bucket/prefix/     path-forwarded bucket / MinIO (auto path-style)

EXAMPLES:
   oss ls                                          list all buckets
   oss ls s3://mybucket/logs/ -n 50                first 50 entries (next run auto-resumes)
   oss ls s3://mybucket/logs/ -d                   directories only (PRE = common prefix)
   oss ls s3://mybucket/logs/ -f --include "*.gz"  files only with a glob filter
   oss ls s3://mybucket -r -a -j > list.ndjson     flat full listing as NDJSON
   oss ls s3://mybucket --export list.xlsx         export the file list to Excel

OUTPUT:
   PRE = common prefix (directory); colored automatically on interactive
   terminals, plain text when piped.

PAGING & CACHE:
   Only --limit entries are shown by default; rerun the same command to
   continue from the breakpoint. Fetched listings are cached locally
   (24h TTL). --reset clears the cache, --no-cache disables it,
   --all streams everything.

EXPORT:
   --export writes the full filtered list (ignores --limit); format by
   extension: txt csv xlsx yaml/yml md.

To download files use:
   oss cp -r s3://mybucket/logs/ ./logs --include "*.gz"   filtered batch download`),
		Description: T(`参数分类:
   过滤: --prefix  --delimiter  -r  -d  -f  --include  --exclude
   分页: -n/--limit  --all  --page-size  --next-token  --reset  --no-cache  --start-after
   输出: -j/--json  --bytes
   导出: --export <file>（下载文件请用 cp）`,
			`Flag groups:
   filter:  --prefix  --delimiter  -r  -d  -f  --include  --exclude
   paging:  -n/--limit  --all  --page-size  --next-token  --reset  --no-cache  --start-after
   output:  -j/--json  --bytes
   export:  --export <file> (to download files use cp)`),
		Flags:  flags,
		Action: runLS,
	}
}

func parseColorPref(s string) colorMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "always", "yes", "true", "1":
		return colorAlways
	case "never", "no", "false", "0":
		return colorNever
	default:
		return colorAuto
	}
}

func runLS(ctx context.Context, c *cli.Command) error {
	o := connOpts(c)
	t, err := s3x.ParseTarget(c.Args().First(), o)
	if err != nil {
		return err
	}
	cl, err := s3x.New(ctx, o, t)
	if err != nil {
		return err
	}
	if t == nil {
		return listBuckets(ctx, c, cl)
	}
	if t.Bucket == "" {
		return errors.New(T("无法从目标解析出桶名", "cannot determine bucket name from target"))
	}

	if p := c.String("export"); p != "" {
		return runExport(ctx, c, cl, t, p)
	}
	return listObjects(ctx, c, cl, t)
}

func listBuckets(ctx context.Context, c *cli.Command, cl *s3x.Client) error {
	out, err := cl.S3.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return apiErr(err, cl.Anonymous)
	}
	stdout := bufio.NewWriter(os.Stdout)
	defer stdout.Flush()

	if c.Bool("json") {
		for _, b := range out.Buckets {
			line, _ := sonic.Marshal(map[string]any{
				"type":          "bucket",
				"name":          aws.ToString(b.Name),
				"creation_date": b.CreationDate,
			})
			stdout.Write(line)
			stdout.WriteByte('\n')
		}
		return nil
	}
	for _, b := range out.Buckets {
		fmt.Fprintf(stdout, "%s  %s\n",
			cDim(fmt.Sprintf("%19s", humanTime(b.CreationDate))),
			cGreen(aws.ToString(b.Name)))
	}
	return nil
}

func listObjects(ctx context.Context, c *cli.Command, cl *s3x.Client, t *s3x.Target) error {
	dirsOnly := c.Bool("dirs")
	filesOnly := c.Bool("files")
	if dirsOnly && filesOnly {
		return errors.New(T("--dirs 与 --files 不能同时使用", "--dirs and --files cannot be used together"))
	}

	p := resolveListParams(c, t)
	ef := newEntryFilter(c, p)

	all := c.Bool("all")
	limit := c.Int64("limit")
	jsonMode := c.Bool("json")
	rawBytes := c.Bool("bytes")

	// ---------- cache setup ----------
	cacheEnabled := !all && !c.Bool("no-cache")
	var (
		cache       listingCache
		fingerprint string
		lc          *listingCacheEntry
	)
	if cacheEnabled {
		cache = loadListingCache()
		fingerprint = listingFingerprint(cl, t, p.prefix, p.delimStr, p.startAfter)
		if c.Bool("reset") {
			if _, ok := cache[fingerprint]; ok {
				delete(cache, fingerprint)
				cache.save()
			}
		} else if !c.IsSet("next-token") && t.List.ContinuationToken == "" {
			lc = cache[fingerprint]
		}
	}

	stdout := bufio.NewWriter(os.Stdout)

	var (
		shown, total int64
		totalSize    int64
	)

	// emitEntry renders one entry; returns true when the display budget is hit.
	emitEntry := func(e cachedEntry) bool {
		if !ef.visible(e) {
			return false
		}
		if jsonMode {
			if e.Dir {
				line, _ := sonic.Marshal(map[string]any{"type": "prefix", "key": e.Key})
				stdout.Write(line)
			} else {
				line, _ := sonic.Marshal(map[string]any{
					"type":          "object",
					"key":           e.Key,
					"size":          e.Size,
					"last_modified": nilIfZeroUnix(e.Mod),
					"etag":          strings.Trim(e.ETag, "\""),
					"storage_class": e.SC,
				})
				stdout.Write(line)
				totalSize += e.Size
			}
			stdout.WriteByte('\n')
		} else if e.Dir {
			fmt.Fprintln(stdout, renderDirRow(e.Key))
		} else {
			fmt.Fprintln(stdout, renderFileRow(entryToObject(e), rawBytes))
			totalSize += e.Size
		}
		shown++
		total++
		return !all && shown >= limit
	}

	useV1 := false
	token := c.String("next-token")
	if token == "" {
		token = t.List.ContinuationToken
	}

	var (
		hitLimit  bool
		lastToken string
	)

	// ---------- 1) serve from cache ----------
	if lc != nil && !lc.Complete {
		fmt.Fprintln(os.Stderr, eYellow(T(
			"… 正在从缓存断点继续（--reset 可清除缓存从头列举）",
			"… continuing from the cached breakpoint (--reset clears the cache)")))
	}
	if lc != nil {
		if lc.Complete {
			idx := lc.Cursor
			for ; idx < len(lc.Entries); idx++ {
				if emitEntry(lc.Entries[idx]) {
					lc.Cursor = idx + 1
					cache[fingerprint] = lc
					cache.save()
					hitLimit = true
					break
				}
			}
			if !hitLimit {
				lc.Cursor = 0
				cache.save()
			}
		} else {
			consumed := 0
			for i, e := range lc.Pending {
				if emitEntry(e) {
					lc.appendEntries(lc.Pending[:i+1])
					lc.Pending = lc.Pending[i+1:]
					cache[fingerprint] = lc
					cache.save()
					hitLimit = true
					break
				}
				consumed = i + 1
			}
			if !hitLimit {
				lc.appendEntries(lc.Pending[:consumed])
				lc.Pending = nil
				token = lc.Token
				useV1 = lc.V1
			}
		}
	}

	// The cached stream is fully consumed when there is no pending entry and
	// no continuation token left. Normalize the state: a full snapshot can be
	// marked Complete (replayable); an over-cap one is dropped so the next
	// run starts fresh.
	if lc != nil && !lc.Complete && !hitLimit && token == "" && len(lc.Pending) == 0 {
		if lc.dropped {
			delete(cache, fingerprint)
			cache.save()
			lc = nil
		} else {
			lc.Complete = true
			lc.Cursor = 0
			cache.save()
			fmt.Fprintln(os.Stderr, eYellow(T(
				"… 缓存中已无更多条目（--reset 清除缓存重新获取）",
				"… no more cached entries (--reset clears the cache)")))
		}
	}

	// ---------- 2) fetch pages from the server ----------
	first := true
	for !hitLimit {
		if lc != nil && lc.Complete {
			break
		}
		if lc != nil && !lc.Complete && token == "" && len(lc.Pending) == 0 && len(lc.Entries) > 0 {
			break
		}
		if ctx.Err() != nil {
			return apiErr(ctx.Err(), cl.Anonymous)
		}
		var (
			pg  *s3x.Page
			err error
		)
		if useV1 {
			marker := token
			if marker == "" {
				marker = t.List.Marker
			}
			pg, err = s3x.ListV1(ctx, cl.S3, p.v1Input(t.Bucket, marker))
		} else {
			pg, err = s3x.ListV2(ctx, cl.S3, p.v2Input(t.Bucket, token))
			if err != nil && first && s3x.V2Unsupported(err) {
				useV1 = true
				first = false
				continue
			}
		}
		first = false
		if err != nil {
			return apiErr(err, cl.Anonymous)
		}

		pageEntries := pageToEntries(pg)
		stop := false
		for i, e := range pageEntries {
			if emitEntry(e) {
				hitLimit = true
				lastToken = pg.NextToken
				if cacheEnabled {
					if lc == nil {
						lc = &listingCacheEntry{}
					}
					// Record the consumed head of the page so the eventual
					// Complete snapshot covers the whole listing.
					lc.appendEntries(pageEntries[:i+1])
					lc.Pending = pageEntries[i+1:]
					lc.Token = pg.NextToken
					lc.V1 = useV1
					lc.UpdatedAt = time.Now()
					cache[fingerprint] = lc
					cache.save()
				}
				stop = true
				break
			}
		}
		if stop {
			break
		}

		if cacheEnabled {
			if lc == nil {
				lc = &listingCacheEntry{}
			}
			lc.appendEntries(pageEntries)
			lc.Token = pg.NextToken
			lc.V1 = useV1
		}
		if !pg.Truncated || pg.NextToken == "" {
			lastToken = ""
			if cacheEnabled && lc != nil && !lc.dropped {
				lc.Complete = true
				lc.Token = ""
				lc.Cursor = 0
			}
			break
		}
		token = pg.NextToken
	}

	if cacheEnabled && lc != nil {
		lc.UpdatedAt = time.Now()
		cache[fingerprint] = lc
		cache.save()
	}

	if err := stdout.Flush(); err != nil {
		return err
	}

	if !jsonMode {
		switch {
		case hitLimit && cacheEnabled:
			fmt.Fprintf(os.Stderr, "\n%s\n", eYellow(fmt.Sprintf(
				T("… 已达 --limit %d（本次显示 %d 条）；再次运行同一命令自动继续（--reset 清除缓存，--all 列举全部）",
					"… reached --limit %d (%d entries shown); rerun the same command to continue (--reset clears the cache, --all lists everything)"),
				limit, shown)))
		case hitLimit && lastToken != "":
			fmt.Fprintf(os.Stderr, "\n%s\n", eYellow(fmt.Sprintf(
				T("… 已达 --limit %d；使用 --next-token %q 继续（或去掉 --no-cache 启用自动续传）",
					"… reached --limit %d; continue with --next-token %q (or drop --no-cache for auto-resume)"),
				limit, lastToken)))
		case hitLimit:
			fmt.Fprintf(os.Stderr, "\n%s\n", eYellow(fmt.Sprintf(
				T("… 已达 --limit %d（本次显示 %d 条）；可能还有更多条目，--all 列举全部",
					"… reached --limit %d (%d entries shown); there may be more, use --all for everything"),
				limit, shown)))
		case all:
			fmt.Fprintf(os.Stderr, "\n%s\n", eGreen(fmt.Sprintf(
				T("共 %d 个对象，总大小 %s", "%d objects, %s total"),
				total, humanSize(totalSize, false))))
		}
	}
	return nil
}

// nilIfZeroUnix returns a time.Time for JSON marshaling, or nil when unset.
func nilIfZeroUnix(mod int64) any {
	if mod == 0 {
		return nil
	}
	return time.Unix(mod, 0)
}

// renderDirRow renders a directory (common prefix) row, left-aligned:
// "PRE  name/". (Reserving the date/size columns for directories wasted
// ~20 leading spaces on every row.)
func renderDirRow(key string) string {
	return fmt.Sprintf("%s  %s", cBlue(fmt.Sprintf("%-4s", "PRE")), dirKey(key))
}

// renderFileRow renders an object row with fixed-width, color-safe columns.
func renderFileRow(o types.Object, rawBytes bool) string {
	var size int64
	if o.Size != nil {
		size = *o.Size
	}
	date := cDim(fmt.Sprintf("%19s", humanTime(o.LastModified)))
	var sizeField string
	if rawBytes {
		sizeField = fmt.Sprintf("%-12s", humanSize(size, true))
	} else {
		sizeField = sizeColored(fmt.Sprintf("%-12s", humanSize(size, false)), size)
	}
	return fmt.Sprintf("%s  %s  %s", date, sizeField, aws.ToString(o.Key))
}
