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
	errs "github.com/ejfkdev/xyz-go/errors"
	"github.com/ejfkdev/xyz-go/registry"
	"github.com/ejfkdev/xyz-go/spec"

	"github.com/ejfkdev/oss/internal/s3x"
)

// apixLsEntry is one listing entry: a bucket, a common prefix or an object.
type apixLsEntry struct {
	Type         string     `json:"type"` // bucket | prefix | object
	Key          string     `json:"key,omitempty"`
	Name         string     `json:"name,omitempty"`
	Size         *int64     `json:"size,omitempty"`
	LastModified *time.Time `json:"last_modified,omitempty"`
	ETag         string     `json:"etag,omitempty"`
	StorageClass string     `json:"storage_class,omitempty"`
	CreationDate *time.Time `json:"creation_date,omitempty"`
}

type apixLsResp struct {
	Target    string        `json:"target,omitempty"`
	Entries   []apixLsEntry `json:"entries"`
	Shown     int           `json:"shown"`
	NextToken string        `json:"next_token,omitempty"`
	Truncated bool          `json:"truncated"`
}

// entryToApix maps an internal cached entry onto the API shape.
func entryToApix(e cachedEntry) apixLsEntry {
	if e.Dir {
		return apixLsEntry{Type: "prefix", Key: e.Key}
	}
	ent := apixLsEntry{
		Type: "object", Key: e.Key, Size: &e.Size,
		ETag: strings.Trim(e.ETag, `"`), StorageClass: e.SC,
	}
	if e.Mod > 0 {
		mod := time.Unix(e.Mod, 0)
		ent.LastModified = &mod
	}
	return ent
}

// apixLsArgs lists a bucket, a prefix window or all visible buckets. One
// definition drives the CLI (streaming, colored), the HTTP route and the MCP
// tool; the json:"-" fields below are CLI plumbing only.
type apixLsArgs struct {
	AK        string        `json:"ak,omitempty" desc:"访问密钥 ID / access key id" secret:"true" http:"header" httpName:"X-Oss-Ak"`
	SK        string        `json:"sk,omitempty" desc:"访问密钥 / secret key" secret:"true" http:"header" httpName:"X-Oss-Sk"`
	Token     string        `json:"token,omitempty" desc:"STS 会话令牌 / session token" secret:"true" http:"header" httpName:"X-Oss-Token"`
	Profile   string        `json:"profile,omitempty" desc:"AWS 共享配置 profile / shared config profile" http:"header" httpName:"X-Oss-Profile"`
	Anonymous bool          `json:"anonymous,omitempty" desc:"强制匿名访问 / force anonymous"`
	Provider  string        `json:"provider,omitempty" desc:"云厂商 / provider"`
	Endpoint  string        `json:"endpoint,omitempty" desc:"自定义 S3 端点 / custom endpoint"`
	Region    string        `json:"region,omitempty" desc:"地域 / region"`
	PathStyle bool          `json:"path-style,omitempty" desc:"路径风格寻址 / path-style addressing"`
	Bucket    string        `json:"bucket,omitempty" desc:"桶名（配合 provider 使用）/ bucket name"`
	Proxy     string        `json:"proxy,omitempty" desc:"HTTP(S) 代理 / proxy"`
	Headers   []string      `json:"headers,omitempty" desc:"附加请求头，Key: Value / extra headers"`
	Insecure  bool          `json:"insecure,omitempty" desc:"跳过 TLS 校验 / skip TLS verification"`
	Timeout   time.Duration `json:"timeout,omitempty" desc:"请求超时，如 15s / request timeout"`

	Target     string   `json:"target,omitempty" desc:"桶或前缀目标（s3://… 或 URL；留空则列出桶）/ bucket or prefix target; omit to list buckets" cli:"positional"`
	Prefix     string   `json:"prefix,omitempty" desc:"键前缀 / key prefix"`
	Delimiter  *string  `json:"delimiter,omitempty" desc:"目录分隔符（默认 /；空串递归平铺）/ delimiter"`
	Recursive  bool     `json:"recursive,omitempty" desc:"递归平铺前缀 / recursive flat listing"`
	Dirs       bool     `json:"dirs,omitempty" desc:"只列目录 / directories only"`
	Files      bool     `json:"files,omitempty" desc:"只列文件 / files only"`
	Include    []string `json:"include,omitempty" desc:"包含 glob（可多个）/ include globs"`
	Exclude    []string `json:"exclude,omitempty" desc:"排除 glob（可多个）/ exclude globs"`
	Limit      int64    `json:"limit,omitempty" desc:"本次最多返回条目数 / max entries" default:"1000"`
	All        bool     `json:"all,omitempty" desc:"返回全部匹配条目（不受 limit 限制）/ fetch everything"`
	PageSize   *int64   `json:"page-size,omitempty" desc:"每页请求大小 / server page size"`
	NextToken  string   `json:"next-token,omitempty" desc:"分页续传令牌 / pagination token"`
	Reset      bool     `json:"reset,omitempty" desc:"清除缓存并重取（仅 CLI）"`
	NoCache    bool     `json:"no-cache,omitempty" desc:"绕过列表缓存（仅 CLI）"`
	StartAfter string   `json:"start-after,omitempty" desc:"从此键之后开始 / start after key"`
	Bytes      bool     `json:"bytes,omitempty" desc:"大小显示为字节数（仅 CLI）"`
	Export     string   `json:"export,omitempty" desc:"导出筛选结果到文件（仅 CLI，格式按扩展名）"`
	Color      string   `json:"color,omitempty" desc:"彩色输出 auto|always|never（仅 CLI）" default:"auto"`
	JSON       bool     `json:"-"`
	CLI        bool     `json:"-"`
}

func registerApixLs(reg *registry.Registry) error {
	_, err := spec.Define("ls", apixLs).
		Summary(T("列出桶/目录/对象（只看不传）", "list buckets, prefixes or objects (read-only)")).
		Description(T("列举桶、目录前缀或对象：过滤、分页、缓存与导出。下载/上传请使用 cp。",
			"Lists buckets, prefixes or objects: filter, page, cache, export. Use cp to download/upload.")).
		CLI(spec.CliHints{
			Usage:   "ls <target>",
			Aliases: []string{"list"},
			Fields: apixConnShortcuts(map[string]spec.CliFieldHint{
				"recursive": {Shorthand: "r"}, "dirs": {Shorthand: "d"}, "files": {Shorthand: "f"},
				"limit": {Shorthand: "n"}, "all": {Shorthand: "a"},
			}),
			After: T(`目标写法:
   s3://bucket/prefix/             scheme 写法（也支持 oss:// cos:// obs://）
   mybucket/prefix/                裸桶名（配合 --provider 或环境变量）
   https://bucket.s3.us-east-1.amazonaws.com/logs/?prefix=2026/&delimiter=/
                                   完整 URL；?prefix/delimiter/max-keys 等查询参数即过滤条件
   https://host/bucket/prefix/     目录转发桶 / MinIO（自动 path-style）

示例:
   oss ls                                         列出所有桶
   oss ls s3://mybucket/logs/ -n 50               列前 50 条（再次运行自动继续）
   oss ls s3://mybucket/logs/ -d                  只列目录（PRE=目录/公共前缀）
   oss ls s3://mybucket/logs/ -f --include "*.gz" 只列文件并过滤
   oss ls s3://mybucket -r -a -j > list.ndjson    全量平铺导出 NDJSON
   oss ls s3://mybucket --export list.xlsx        导出文件列表为 Excel

输出说明:
   PRE = 公共前缀（目录）；交互终端自动彩色，管道输出纯文本。

分页与缓存:
   默认只显示 --limit 条，重复运行同一命令自动从断点继续；已获取列表缓存于本地
   (24h)。--reset 清缓存重取，--no-cache 禁缓存，--all 全量流式列举。

导出:
   --export 按前缀与过滤条件导出完整列表（不受 --limit 限制），
   格式由扩展名决定: txt csv xlsx yaml/yml md。

下载请用:  oss cp -r s3://mybucket/logs/ ./logs --include "*.gz"`,
				`TARGETS:
   s3://bucket/prefix/             scheme style (also oss:// cos:// obs://)
   mybucket/prefix/                bare bucket (with --provider or env vars)
   https://bucket.s3.us-east-1.amazonaws.com/logs/?prefix=2026/&delimiter=/
                                   full URL; ?prefix/delimiter/max-keys act as filters
   https://host/bucket/prefix/     path-forwarded bucket / MinIO (auto path-style)

EXAMPLES:
   oss ls                                         list all buckets
   oss ls s3://mybucket/logs/ -n 50               first 50 entries (next run auto-resumes)
   oss ls s3://mybucket/logs/ -d                  directories only (PRE = common prefix)
   oss ls s3://mybucket/logs/ -f --include "*.gz" files only with a glob filter
   oss ls s3://mybucket -r -a -j > list.ndjson    flat full listing as NDJSON
   oss ls s3://mybucket --export list.xlsx        export the file list to Excel

OUTPUT:
   PRE = common prefix (directory); colored on interactive terminals,
   plain text when piped.

PAGING & CACHE:
   Only --limit entries are shown by default; rerun the same command to
   continue from the breakpoint. Fetched listings are cached locally
   (24h TTL). --reset clears the cache, --no-cache disables it, --all streams.

EXPORT:
   --export writes the full filtered list (ignores --limit); format by
   extension: txt csv xlsx yaml/yml md.

To download files use:
   oss cp -r s3://mybucket/logs/ ./logs --include "*.gz"   filtered batch download`),
		}).
		HTTP(xyzHintsGET("/ls")).
		MCP(xyzMCPRead()).
		Register(reg)
	return err
}

func apixLs(ctx context.Context, in *apixLsArgs) (*apixLsResp, error) {
	if in.CLI {
		return nil, lsCLI(ctx, in)
	}
	if in.Dirs && in.Files {
		return nil, errs.New(errs.KindInvalidInput, T("--dirs 与 --files 不能同时使用", "--dirs and --files cannot be used together"))
	}
	if in.Limit <= 0 && !in.All {
		return nil, errs.New(errs.KindInvalidInput, T("limit 必须大于 0", "limit must be greater than 0"))
	}
	o := in.connOpts()
	t, err := s3x.ParseTarget(in.Target, o)
	if err != nil {
		return nil, apixErr(false, err)
	}
	cl, err := s3x.New(ctx, o, t)
	if err != nil {
		return nil, apixErr(false, err)
	}
	resp := &apixLsResp{Target: in.Target}

	// No target at all: list the buckets visible to the connection.
	if t == nil {
		out, err := cl.S3.ListBuckets(ctx, &s3.ListBucketsInput{})
		if err != nil {
			return nil, apixErr(cl.Anonymous, err)
		}
		for _, b := range out.Buckets {
			resp.Entries = append(resp.Entries, apixLsEntry{
				Type: "bucket", Name: aws.ToString(b.Name), CreationDate: b.CreationDate,
			})
		}
		resp.Shown = len(resp.Entries)
		return resp, nil
	}
	if t.Bucket == "" {
		return nil, errs.New(errs.KindInvalidInput, T("无法从目标解析出桶名", "cannot determine bucket name from target"))
	}

	p, ef := in.lsParams(t)
	entries, nextToken, truncated, err := listWindow(ctx, cl, t, p, ef, in.Limit, in.All, in.NextToken)
	if err != nil {
		return nil, apixErr(cl.Anonymous, err)
	}
	for _, e := range entries {
		resp.Entries = append(resp.Entries, entryToApix(e))
	}
	resp.Shown = len(resp.Entries)
	resp.NextToken = nextToken
	resp.Truncated = truncated
	return resp, nil
}

// connOpts builds the connection options from the inline fields.
func (in *apixLsArgs) connOpts() *s3x.ConnOpts {
	return connOptsFrom(in.AK, in.SK, in.Token, in.Profile, in.Provider, in.Endpoint, in.Region,
		in.Proxy, in.Bucket, in.Anonymous, in.PathStyle, in.Insecure, in.Headers, in.Timeout)
}

// lsParams resolves the framework-free listing parameters from the args.
func (in *apixLsArgs) lsParams(t *s3x.Target) (listParams, entryFilter) {
	f := listFlags{
		recursive:  in.Recursive,
		prefix:     in.Prefix,
		startAfter: in.StartAfter,
		dirsOnly:   in.Dirs,
		filesOnly:  in.Files,
		include:    in.Include,
		exclude:    in.Exclude,
	}
	if in.Delimiter != nil {
		f.delimiter = in.Delimiter
	}
	if in.PageSize != nil {
		f.pageSize = in.PageSize
	}
	p := resolveListParamsF(f, t)
	return p, newEntryFilterF(f, p)
}

// ------------------------------------------------------------------ CLI side

// lsCLI is the command-line behavior of ls: streamed, colored rows, NDJSON,
// cache resume and export — the same UX as before the CLI migration.
func lsCLI(ctx context.Context, in *apixLsArgs) error {
	o := in.connOpts()
	t, err := s3x.ParseTarget(in.Target, o)
	if err != nil {
		return err
	}
	cl, err := s3x.New(ctx, o, t)
	if err != nil {
		return err
	}
	if t == nil {
		return listBuckets(ctx, in.JSON, cl)
	}
	if t.Bucket == "" {
		return errors.New(T("无法从目标解析出桶名", "cannot determine bucket name from target"))
	}
	p, ef := in.lsParams(t)
	if in.Export != "" {
		return runExport(ctx, cl, t, p, ef, in.Export, !in.NoCache && !in.Reset)
	}
	return listObjects(ctx, cl, t, p, ef, lsOpts{
		limit: in.Limit, all: in.All, jsonMode: in.JSON, rawBytes: in.Bytes,
		reset: in.Reset, noCache: in.NoCache, nextToken: in.NextToken,
	})
}

func listBuckets(ctx context.Context, jsonMode bool, cl *s3x.Client) error {
	out, err := cl.S3.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return apiErr(err, cl.Anonymous)
	}
	stdout := bufio.NewWriter(os.Stdout)
	defer stdout.Flush()

	if jsonMode {
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

// lsOpts carries the CLI-only listing switches (flag-independent shape).
type lsOpts struct {
	limit     int64
	all       bool
	jsonMode  bool
	rawBytes  bool
	reset     bool
	noCache   bool
	nextToken string
}

// setNextToken reports whether the user passed --next-token explicitly
// (mirrors the previous framework's IsSet semantics: an empty value is
// treated as "not set").
func (o lsOpts) setNextToken() bool { return o.nextToken != "" }

func listObjects(ctx context.Context, cl *s3x.Client, t *s3x.Target, p listParams, ef entryFilter, opt lsOpts) error {
	all := opt.all
	limit := opt.limit
	jsonMode := opt.jsonMode
	rawBytes := opt.rawBytes

	// ---------- cache setup ----------
	cacheEnabled := !all && !opt.noCache
	var (
		cache       listingCache
		fingerprint string
		lc          *listingCacheEntry
	)
	if cacheEnabled {
		cache = loadListingCache()
		fingerprint = listingFingerprint(cl, t, p.prefix, p.delimStr, p.startAfter)
		if opt.reset {
			if _, ok := cache[fingerprint]; ok {
				delete(cache, fingerprint)
				cache.save()
			}
		} else if !opt.setNextToken() && t.List.ContinuationToken == "" {
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
	token := opt.nextToken
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
	// no continuation token left.
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
