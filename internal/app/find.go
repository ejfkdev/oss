package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/bytedance/sonic"
	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"

	"github.com/ejfkdev/oss/internal/s3x"
)

// Bucket-existence states derived from probe responses. Probes send an
// anonymous ListObjects request, so a single request reveals both existence
// and whether anonymous directory listing is allowed. Best-effort OSINT:
// providers answer differently, results are indications, not guarantees.
const (
	findListable = "listable" // exists AND anonymously listable
	findExists   = "exists"   // exists but NOT anonymously listable
	findNotFound = "notfound" // 404 / NXDOMAIN
	findUnknown  = "unknown"  // timeout / unexpected status
)

// findResult is one probe outcome.
type findResult struct {
	Input    string `json:"input"`
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Region   string `json:"region,omitempty"`
	URL      string `json:"url"` // bucket root URL
	Status   int    `json:"status,omitempty"`
	State    string `json:"state"` // listable | exists | notfound | unknown
	Detail   string `json:"detail"`
	Listable bool   `json:"listable"`
	// Targeted marks probes aimed at an endpoint the user gave explicitly
	// (a full bucket URL), as opposed to the broad bare-name fan-out.
	Targeted bool `json:"-"`
}

func findCmd() *cli.Command {
	flags := append([]cli.Flag{
		&cli.IntFlag{Name: "jobs", Value: 0, Usage: T("并发探测数（默认全部并发）", "concurrent probes (default: all at once)")},
		&cli.BoolFlag{Name: "cn", Usage: T("只探测中国大陆+港台地域（默认行为）", "probe only mainland-China + HK/TW regions (the default)")},
		&cli.BoolFlag{Name: "global", Usage: T("探测全部地域（含海外）", "probe all regions (including overseas)")},
		&cli.BoolFlag{Name: "listable", Aliases: []string{"l"}, Usage: T("切换为「发现可匿名列目录的桶」模式：只输出可匿名列目录的命中（默认模式输出所有存储命中）", "switch to the 'anonymously listable' mode: only print anonymously listable hits (the default mode prints every storage hit)")},
		&cli.BoolFlag{Name: "json", Aliases: []string{"j"}, Usage: T("NDJSON 输出（每个探测一行 + 汇总行）", "NDJSON output (one line per probe + a summary line)")},
		&cli.StringFlag{Name: "export", Usage: T("导出结果到文件，格式按扩展名 .txt .csv .xlsx .yaml .md；含 listable_url 字段存可匿名列桶的完整 URL", "export results to a file, format by extension .txt .csv .xlsx .yaml .md; includes a listable_url field holding full URLs of anonymously listable buckets")},
	}, connFlags()...)
	return &cli.Command{
		Name:    "find",
		Aliases: []string{"which"},
		Usage:   T("查找桶存在于哪些云存储，并识别能否匿名列目录", "find which cloud storage hosts a bucket and whether anonymous listing is allowed"),
		UsageText: T(`oss find <桶名|URL> [...]  或  cat list.txt | oss find

支持批量：命令行给多个参数，和/或从 stdin 一行一个读取（可混用）。
每个输入可以是：
   - 桶名（如 mybucket）→ 并发探测所有已知云厂商的常用区域
   - 完整桶 URL/路径（如 https://mybucket.s3.us-east-1.amazonaws.com/prefix
     或 s3://mybucket/key）→ 只探测该端点（更精确）

探测方式：向桶发 ListObjects 请求，一次请求同时判断存在性 + 能否列目录。
默认匿名探测；配置了凭证（--ak/--sk，STS 加 --token；或 OSS_*/AWS_* 环境变量、
--profile，与 ls/cp 相同）时自动改为 SigV4 签名探测，可验证非匿名桶：
   HTTP 200        → 存在且可列目录（匿名模式=可匿名列，亮绿★；签名模式=凭证可列）
   HTTP 3xx        → 存在（重定向）
   HTTP 401/403    → 匿名模式：存在但私有；签名模式：存在但拒绝访问
                     （凭证本身被拒，如 InvalidAccessKeyId，则判为无法判断）
   HTTP 404/域名不存在 → 不存在
   超时/其它状态码   → 无法判断

示例:
   oss find mybucket                       查找单个桶名（命中即逐行流式打印）
   oss find bucket-a bucket-b bucket-c     批量（命令行多个）
   cat buckets.txt | oss find              批量（stdin 一行一个）
   oss find https://mybucket.s3.us-east-1.amazonaws.com/   完整 URL
   oss find mybucket --listable            只列出可匿名列目录的桶
   oss find mybucket -j                    NDJSON 输出
   oss find bucket-a bucket-b --export r.csv   导出 CSV（含 listable_url）
   oss find mybucket --provider aliyun --ak LTAI... --sk ...   用凭证验证阿里云上的私有桶
   OSS_ACCESS_KEY_ID=... OSS_SECRET_ACCESS_KEY=... oss find mybucket --provider tencent

说明:
   - 默认只探测中国大陆+港台地域（--cn 显式指定同效）；--global 探测全部地域（含海外）；
     --region 可只探测指定区域。未找到不代表绝对不存在，可用 --region 或 --global 重试
   - 两种模式：默认「发现桶存储」——存在即命中（含私有）；--listable「发现可匿名
     列目录的桶」——只输出可匿名列目录的命中。两种模式都是只打印命中的结果，
     发现一个命中就立即流式输出一行（含完整访问 URL），未命中的探测不输出
   - 腾讯云桶名需含 APPID 后缀（如 mybucket-1250000000）
   - 不支持七牛：匿名访问一律返回 400，无法判断存在性；B2 恒返回 403、R2 需账号 ID，
     也都不在探测范围
   - 默认匿名探测，不发送任何凭证；配置凭证后自动切换为签名探测——建议同时用
     --provider 限定到凭证所属厂商（签名请求发给其它厂商只会被拒）；
     --anonymous 可强制匿名探测`,
			`oss find <bucket|URL> [...]  or  cat list.txt | oss find

Batch: give multiple arguments, and/or pipe one entry per line via stdin
(both can be combined). Each input can be:
   - a bucket name (e.g. mybucket) -> probe all known providers' regions
   - a full bucket URL/path (e.g. https://mybucket.s3.us-east-1.amazonaws.com/prefix
     or s3://mybucket/key) -> probe only that endpoint (more precise)

Probing: one ListObjects request per bucket reveals both existence and
listability. Probes are anonymous by default; when credentials are configured
(--ak/--sk, plus --token for STS; or OSS_*/AWS_* env vars, --profile — same
as ls/cp) they switch to SigV4-signed probes, which can verify non-anonymous
buckets:
   HTTP 200        -> exists AND listable (anonymous mode: anonymously listable,
                      bright green; signed mode: listable with the credentials)
   HTTP 3xx        -> exists (redirect)
   HTTP 401/403    -> anonymous mode: exists but private; signed mode: exists
                      but denied (credentials themselves rejected, e.g.
                      InvalidAccessKeyId, is treated as inconclusive)
   HTTP 404 / no such host -> not found
   timeout / other status  -> inconclusive

EXAMPLES:
   oss find mybucket                       find a single bucket name (hits stream as found)
   oss find bucket-a bucket-b bucket-c     batch (multiple arguments)
   cat buckets.txt | oss find              batch (stdin, one per line)
   oss find https://mybucket.s3.us-east-1.amazonaws.com/   full URL
   oss find mybucket --listable            list only anonymously listable buckets
   oss find mybucket -j                    NDJSON output
   oss find bucket-a bucket-b --export r.csv   export CSV (with listable_url)
   oss find mybucket --provider aliyun --ak LTAI... --sk ...   verify a private Aliyun bucket with credentials
   OSS_ACCESS_KEY_ID=... OSS_SECRET_ACCESS_KEY=... oss find mybucket --provider tencent

NOTES:
   - By default only mainland-China + HK/TW regions are probed (--cn is the
     same); --global probes all regions (incl. overseas); --region probes a
     single region. "not found" is not a guarantee — retry with --region/--global
   - Two modes: the default "find bucket storage" mode treats any existing
     bucket as a hit (private included); --listable switches to the "find
     anonymously listable buckets" mode, printing only listable hits. Both
     modes print only matches, streamed one line per hit (with the full access
     URL); probes that find nothing are not printed at all
   - Tencent COS bucket names include the APPID suffix (e.g. mybucket-1250000000)
   - Qiniu is not supported (anonymous requests always return 400, so existence
     cannot be determined); B2 always returns 403 and R2 needs an account ID,
     so none of these are probed
   - Probes are anonymous by default and send no credentials; when credentials
     are configured they switch to signed probing — combine with --provider to
     restrict the scan to the provider the credentials belong to (signed
     requests sent to other providers are simply rejected); --anonymous forces
     anonymous probing`),
		Flags:  flags,
		Action: runFind,
	}
}

// collectFindInputs gathers inputs from CLI args and stdin (one per line).
func collectFindInputs(c *cli.Command) []string {
	inputs := append([]string{}, c.Args().Slice()...)
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line != "" {
				inputs = append(inputs, line)
			}
		}
	}
	seen := map[string]bool{}
	uniq := make([]string, 0, len(inputs))
	for _, in := range inputs {
		in = strings.TrimSpace(in)
		if in == "" || seen[in] {
			continue
		}
		seen[in] = true
		uniq = append(uniq, in)
	}
	return uniq
}

// buildFindJobs turns inputs into probe jobs. Bare bucket names fan out to
// every known provider (or only the --provider one when it is set); full
// URLs/paths probe only the parsed endpoint.
// global=true probes every region; otherwise only CN regions (mainland
// China + HK/TW) are probed. regionOverride, when set, takes precedence.
func buildFindJobs(inputs []string, o *s3x.ConnOpts, regionOverride string, global bool) (jobs []findResult, invalid map[string]string) {
	type job struct {
		input    string
		provider string
		name     string
		region   string
		url      string
		targeted bool // endpoint given explicitly by the user (full URL input)
	}
	var js []job
	invalid = map[string]string{}
	onlyProvider := strings.ToLower(strings.TrimSpace(o.Provider))

	for _, in := range inputs {
		t, err := s3x.ParseTarget(in, o)
		if err != nil || t == nil || t.Bucket == "" {
			invalid[in] = T("无法解析", "unparseable")
			continue
		}
		if t.Endpoint != "" {
			root := s3x.BucketRootURL(t)
			if root == "" {
				invalid[in] = T("无法构造桶 URL", "cannot build bucket URL")
				continue
			}
			prov := t.Provider
			name := s3x.ProviderDisplayName(prov)
			if prov == "" {
				if u, e := url.Parse(t.Endpoint); e == nil {
					name = u.Host
				}
			}
			js = append(js, job{input: in, provider: prov, name: name, region: t.Region, url: root, targeted: true})
			continue
		}
		// Bare bucket name: fan out to all providers.
		if !s3x.ValidBucketName(t.Bucket) {
			invalid[in] = T("无效桶名", "invalid bucket name")
			continue
		}
		for _, p := range s3x.ScanProbes {
			if onlyProvider != "" && p.Provider != onlyProvider {
				continue
			}
			if len(p.Regions) == 0 {
				// Region-less probe (e.g. AWS international global endpoint,
				// GCS, Yandex): only probed with --global and no --region.
				if global && regionOverride == "" {
					js = append(js, job{input: in, provider: p.Provider, name: p.Name, region: "", url: p.ScanURLs(t.Bucket, nil)[0]})
				}
				continue
			}
			var regions []string
			switch {
			case regionOverride != "":
				regions = []string{regionOverride}
			case global:
				regions = p.Regions
			default: // default: CN only (mainland + HK/TW)
				regions = p.CNRegions
			}
			if len(regions) == 0 {
				continue // e.g. --cn for a provider with no CN regions
			}
			urls := p.ScanURLs(t.Bucket, regions)
			for i, u := range urls {
				js = append(js, job{input: in, provider: p.Provider, name: p.Name, region: regions[i], url: u})
			}
		}
	}

	for _, j := range js {
		jobs = append(jobs, findResult{
			Input: j.input, Provider: j.provider, Name: j.name, Region: j.region, URL: j.url,
			Targeted: j.targeted,
		})
	}
	return jobs, invalid
}

// probeURL appends a ListObjects query (max-keys=1) to a bucket root URL.
func probeURL(root string) string {
	sep := "?"
	if strings.Contains(root, "?") {
		sep = "&"
	}
	return root + sep + "max-keys=1"
}

// probeBucket sends one anonymous ListObjects request and classifies the
// outcome, returning existence + anonymous-listability together. The response
// body is read (up to 8 KiB) so provider-specific error formats (e.g. UCloud
// JSON RetCode) can be parsed instead of relying on HTTP status alone.
func probeBucket(ctx context.Context, hc *http.Client, root string) (status int, state, detail string, listable bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL(root), nil)
	if err != nil {
		return 0, findUnknown, err.Error(), false
	}
	resp, err := hc.Do(req)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			return 0, findNotFound, T("域名不存在", "no such host"), false
		}
		if os.IsTimeout(err) || strings.Contains(err.Error(), "deadline") {
			return 0, findUnknown, T("超时", "timeout"), false
		}
		return 0, findUnknown, T("连接失败", "connection failed"), false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	state, detail, listable = classifyProbe(resp.StatusCode, body)
	return resp.StatusCode, state, detail, listable
}

// classifyProbe interprets the HTTP status plus the response body. Some
// providers (notably UCloud) answer bucket-level requests with a JSON body
// carrying a RetCode instead of a meaningful HTTP status, so the body must be
// inspected: RetCode -148653 / "bucket not exists" -> not found;
// RetCode -148643 / "no authorization" -> exists but private.
func classifyProbe(status int, body []byte) (state, detail string, listable bool) {
	// UCloud-style JSON RetCode responses.
	if bytes.Contains(body, []byte("RetCode")) {
		if bytes.Contains(body, []byte("bucket not exists")) || bytes.Contains(body, []byte("-148653")) {
			return findNotFound, T("桶不存在", "bucket not exists"), false
		}
		if bytes.Contains(body, []byte("no authorization")) || bytes.Contains(body, []byte("-148643")) {
			return findExists, T("私有，不可匿名列", "private"), false
		}
		return findUnknown, fmt.Sprintf("HTTP %d (RetCode)", status), false
	}
	switch {
	case status == http.StatusOK || status == http.StatusNoContent:
		// A 200 that is actually an error body (not a listing) means the
		// bucket exists but is not anonymously listable.
		if bytes.Contains(body, []byte("<Error>")) || bytes.Contains(body, []byte("AccessDenied")) ||
			bytes.Contains(body, []byte("ErrMsg")) {
			return findExists, T("私有，不可匿名列", "private"), false
		}
		return findListable, T("可匿名列目录", "anonymous list"), true
	case status >= 300 && status < 400:
		return findExists, T("存在（重定向）", "redirect"), false
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return findExists, T("私有，不可匿名列", "private"), false
	case status == http.StatusNotFound:
		return findNotFound, T("桶不存在", "no such bucket"), false
	default:
		return findUnknown, fmt.Sprintf("HTTP %d", status), false
	}
}

// emptySHA256 is the SHA-256 of an empty payload; SigV4-signed S3 requests
// must carry it in X-Amz-Content-Sha256.
var emptySHA256 = func() string {
	h := sha256.Sum256(nil)
	return hex.EncodeToString(h[:])
}()

// probeBucketSigned sends one SigV4-signed ListObjects request with the
// provided credentials (AK/SK with an optional STS session token). This is
// the credentialed counterpart of probeBucket: it can confirm buckets that
// are not anonymously accessible.
func probeBucketSigned(ctx context.Context, hc *http.Client, creds aws.Credentials, region, root string, targeted bool) (status int, state, detail string, listable bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL(root), nil)
	if err != nil {
		return 0, findUnknown, err.Error(), false
	}
	req.Header.Set("X-Amz-Content-Sha256", emptySHA256)
	signRegion := region
	if signRegion == "" {
		signRegion = "us-east-1"
	}
	signer := v4.NewSigner()
	if err := signer.SignHTTP(ctx, creds, req, emptySHA256, "s3", signRegion, time.Now()); err != nil {
		return 0, findUnknown, err.Error(), false
	}
	resp, err := hc.Do(req)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			return 0, findNotFound, T("域名不存在", "no such host"), false
		}
		if os.IsTimeout(err) || strings.Contains(err.Error(), "deadline") {
			return 0, findUnknown, T("超时", "timeout"), false
		}
		return 0, findUnknown, T("连接失败", "connection failed"), false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	state, detail, listable = classifySignedProbe(resp.StatusCode, body, targeted)
	return resp.StatusCode, state, detail, listable
}

// classifySignedProbe interprets responses to credentialed probes. targeted
// tells whether the probe was aimed at an endpoint the user chose explicitly
// (--provider, or a full bucket URL): only then does a plain denial mean
// "bucket exists but these credentials lack permission"; against the broad
// bare-name fan-out a denial usually just means the credentials belong to
// another provider, which reveals nothing about the bucket.
func classifySignedProbe(status int, body []byte, targeted bool) (state, detail string, listable bool) {
	// UCloud-style JSON RetCode responses.
	if bytes.Contains(body, []byte("RetCode")) {
		if bytes.Contains(body, []byte("bucket not exists")) || bytes.Contains(body, []byte("-148653")) {
			return findNotFound, T("桶不存在", "bucket not exists"), false
		}
		if bytes.Contains(body, []byte("no authorization")) || bytes.Contains(body, []byte("-148643")) {
			return findExists, T("存在·拒绝访问", "exists·denied"), false
		}
		return findUnknown, fmt.Sprintf("HTTP %d (RetCode)", status), false
	}
	switch {
	case status == http.StatusOK || status == http.StatusNoContent:
		if bytes.Contains(body, []byte("<Error>")) || bytes.Contains(body, []byte("AccessDenied")) ||
			bytes.Contains(body, []byte("ErrMsg")) {
			return findExists, T("存在·拒绝访问", "exists·denied"), false
		}
		return findListable, T("可列目录（凭证）", "listable (credentials)"), true
	case status >= 300 && status < 400:
		return findExists, T("存在（重定向）", "redirect"), false
	case status == http.StatusNotFound:
		return findNotFound, T("桶不存在", "no such bucket"), false
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		if credRejected(body) {
			return findUnknown, T("凭证被拒（疑似不适用于该厂商）", "credentials rejected (likely wrong provider)"), false
		}
		if targeted {
			return findExists, T("存在·拒绝访问", "exists·denied"), false
		}
		return findUnknown, T("拒绝访问", "denied"), false
	default:
		return findUnknown, fmt.Sprintf("HTTP %d", status), false
	}
}

// credRejected reports whether an error body shows the credentials themselves
// were rejected (invalid key / bad signature), as opposed to the request being
// denied for an existing bucket. S3-compatible XML errors all carry a <Code>.
func credRejected(body []byte) bool {
	for _, code := range []string{
		"InvalidAccessKeyId", "SignatureDoesNotMatch", "InvalidToken",
		"ExpiredToken", "TokenRefreshRequired", "InvalidSecurity",
		"AuthFailure", "UnrecognizedClientException", "RequestTimeTooSkewed",
	} {
		if bytes.Contains(body, []byte(code)) {
			return true
		}
	}
	return false
}

func runFind(ctx context.Context, c *cli.Command) error {
	o := connOpts(c)

	inputs := collectFindInputs(c)
	if len(inputs) == 0 {
		return errors.New(T(
			"用法: oss find <桶名|URL> [...]（或 cat list.txt | oss find）",
			"usage: oss find <bucket|URL> [...] (or: cat list.txt | oss find)"))
	}

	hc := s3x.NewHTTPClient(o)
	if hc.Timeout == 0 {
		// Some endpoints (notably AWS' global root endpoint) can take well
		// over ten seconds on first contact; keep the default generous so
		// hits aren't lost to timeouts. --timeout still overrides.
		hc.Timeout = 15 * time.Second
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	regionOverride := c.String("region")
	if c.Bool("cn") && c.Bool("global") {
		return errors.New(T("--cn 与 --global 不能同时使用", "--cn and --global are mutually exclusive"))
	}
	global := c.Bool("global") // default (and --cn): CN regions only
	onlyProvider := strings.ToLower(strings.TrimSpace(o.Provider))
	if onlyProvider != "" {
		if _, ok := s3x.Providers[onlyProvider]; !ok {
			return fmt.Errorf("%s (choose from: %s)",
				fmt.Sprintf(T("未知厂商 %q", "unknown provider %q"), onlyProvider),
				strings.Join(s3x.ProviderNames(), ", "))
		}
	}

	// Resolve credentials once. find probes anonymously by default; when
	// credentials are configured (--ak/--sk/--token, OSS_*/AWS_* env vars or
	// --profile) it switches to SigV4-signed probes, which can also confirm
	// non-anonymous buckets. --anonymous forces anonymous probing.
	credProvider, anon, err := s3x.ResolveCredentials(ctx, o, regionOverride)
	if err != nil {
		return err
	}
	var creds aws.Credentials
	if !anon {
		creds, err = credProvider.Retrieve(ctx)
		if err != nil {
			return fmt.Errorf(T("无法获取凭证: %v", "failed to retrieve credentials: %v"), err)
		}
	}

	results, invalid := buildFindJobs(inputs, o, regionOverride, global)
	if len(results) == 0 && len(invalid) == len(inputs) {
		return errors.New(T("没有可探测的输入", "no probeable inputs"))
	}
	if len(results) == 0 && onlyProvider != "" {
		return errors.New(fmt.Sprintf(T("厂商 %q 没有可探测的端点", "provider %q has no probeable endpoints"), onlyProvider))
	}

	// Run all probes concurrently. In human mode, matches are streamed one
	// line per hit the moment each probe resolves — no waiting for the whole
	// scan to finish, and probes that find nothing are never printed.
	//
	// Two modes: default finds bucket storage (exists OR listable hits);
	// --listable finds only anonymously listable buckets.
	listableOnly := c.Bool("listable")
	isHit := func(state string) bool {
		if listableOnly {
			return state == findListable
		}
		return state == findListable || state == findExists
	}
	jsonOut := c.Bool("json")
	exportPath := c.String("export")
	human := !jsonOut && exportPath == ""
	multi := len(inputs) > 1
	var w *tabwriter.Writer
	if human {
		if !anon {
			fmt.Fprintln(os.Stderr, cDim(T(
				"使用提供的凭证进行签名探测（可验证非匿名桶）",
				"signed probing with the provided credentials (can verify non-anonymous buckets)")))
		}
		w = tabwriter.NewWriter(os.Stdout, 1, 2, 2, ' ', 0)
		// Invalid inputs are known up front; report them before probing.
		for _, in := range inputs {
			if msg, bad := invalid[in]; bad {
				if multi {
					fmt.Fprintf(w, "%s\t%s\t%s\n", cGrey("·"), in, cGrey(msg))
				} else {
					fmt.Fprintf(w, "%s\t%s\n", cGrey("·"), msg)
				}
			}
		}
		_ = w.Flush()
	}
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	if n := c.Int("jobs"); n > 0 {
		g.SetLimit(n)
	}
	for i := range results {
		i := i
		g.Go(func() error {
			r := &results[i]
			var status int
			var state, detail string
			var listable bool
			if anon {
				status, state, detail, listable = probeBucket(gctx, hc, r.URL)
			} else {
				status, state, detail, listable = probeBucketSigned(gctx, hc, creds,
					r.Region, r.URL, r.Targeted || onlyProvider != "")
			}
			mu.Lock()
			r.Status, r.State, r.Detail, r.Listable = status, state, detail, listable
			if human && isHit(state) {
				mark, stateText := stateStyle(state, !anon)
				region := ""
				if r.Region != "" {
					region = " @ " + r.Region
				}
				if multi {
					fmt.Fprintf(w, "%s\t%s\t%s%s\t%s\t%s\n",
						mark, cGrey(r.Input), r.Name, region, stateText, r.URL)
				} else {
					fmt.Fprintf(w, "%s\t%s%s\t%s\t%s\n",
						mark, r.Name, region, stateText, r.URL)
				}
				_ = w.Flush()
			}
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()

	// ---- Export mode ----
	if path := c.String("export"); path != "" {
		out := results
		if listableOnly {
			out = nil
			for _, r := range results {
				if r.Listable {
					out = append(out, r)
				}
			}
		}
		if err := exportFindResults(path, inputs, out, invalid); err != nil {
			return err
		}
		fmt.Printf("%s %s\n", checkMarkStdout(),
			fmt.Sprintf(T("已导出 %d 条探测结果 → %s", "exported %d probe result(s) → %s"), len(out), path))
		return nil
	}

	// ---- JSON mode ----
	if c.Bool("json") {
		for i := range results {
			if listableOnly && !results[i].Listable {
				continue
			}
			line, _ := sonic.Marshal(&results[i])
			fmt.Println(string(line))
		}
		// Summary line with the dedicated anonymously-listable URL list.
		listable := make([]map[string]any, 0)
		found := 0
		for i := range results {
			r := &results[i]
			if isHit(r.State) {
				found++
			}
			if r.Listable {
				listable = append(listable, map[string]any{
					"input": r.Input, "provider": r.Provider, "name": r.Name,
					"region": r.Region, "url": r.URL,
				})
			}
		}
		auth := "anonymous"
		if !anon {
			auth = "signed"
		}
		line, _ := sonic.Marshal(map[string]any{
			"auth":               auth,
			"inputs":             len(inputs),
			"found":              found,
			"anonymous_listable": listable,
		})
		fmt.Println(string(line))
		return nil
	}

	// ---- Human-readable tail ----
	// Matches already streamed during probing; print no-hit inputs and the
	// anonymously-listable summary.
	printFindTail(inputs, results, invalid, listableOnly, !anon)
	return nil
}

// stateStyle returns (marker, colored-state-text) for a result state.
// signed selects the wording for credentialed probes.
func stateStyle(state string, signed bool) (string, string) {
	switch state {
	case findListable:
		if signed {
			return cGreenBright("★"), cGreenBright(T("可列目录（凭证）", "listable (credentials)"))
		}
		return cGreenBright("★"), cGreenBright(T("可匿名列目录", "listable"))
	case findExists:
		if signed {
			return cYellow("✓"), cYellow(T("存在·拒绝访问", "exists·denied"))
		}
		return cYellow("✓"), cYellow(T("存在·私有", "exists·private"))
	case findNotFound:
		return cGrey("✗"), cGrey(T("未发现", "not found"))
	default:
		return cGrey("?"), cGrey(T("无法判断", "unknown"))
	}
}

// printFindTail finishes the human-readable output: matches were already
// streamed during probing, so this only reports inputs with no hits at all,
// then prints the listable summary. listableOnly and signed select wording
// that matches the active mode.
func printFindTail(inputs []string, results []findResult, invalid map[string]string, listableOnly, signed bool) {
	byInput := make(map[string][]*findResult)
	for i := range results {
		r := &results[i]
		byInput[r.Input] = append(byInput[r.Input], r)
	}

	noHitMsg := T("未在已知厂商中发现（可尝试 --global / --region 扩大范围）",
		"not found on known providers (try --global / --region)")
	if listableOnly {
		if signed {
			noHitMsg = T("未发现可列目录（桶可能不存在，或凭证无权限）",
				"not listable (bucket may not exist, or credentials lack permission)")
		} else {
			noHitMsg = T("未发现可匿名列目录（桶可能不存在，或存在但私有）",
				"not anonymously listable (bucket may not exist, or exists but is private)")
		}
	}

	multi := len(inputs) > 1
	w := tabwriter.NewWriter(os.Stdout, 1, 2, 2, ' ', 0)
	for _, in := range inputs {
		if _, bad := invalid[in]; bad {
			continue // already reported before probing
		}
		matched := 0
		for _, r := range byInput[in] {
			if r.Listable || (!listableOnly && r.State == findExists) {
				matched++
			}
		}
		if matched == 0 {
			if multi {
				fmt.Fprintf(w, "%s\t%s\t%s\n", cGrey("✗"), in, cGrey(noHitMsg))
			} else {
				fmt.Fprintf(w, "%s\t%s\n", cGrey("✗"), cGrey(noHitMsg))
			}
		}
	}
	_ = w.Flush()

	// Summary of listable buckets (the highlight).
	var listable []*findResult
	for i := range results {
		if results[i].Listable {
			listable = append(listable, &results[i])
		}
	}
	foundMsg := T("发现 %d 个可匿名列目录的桶:", "found %d anonymously listable bucket(s):")
	noneMsg := T("未发现可匿名列目录的桶", "no anonymously listable bucket found")
	if signed {
		foundMsg = T("发现 %d 个可列目录的桶（使用提供的凭证）:", "found %d listable bucket(s) with the provided credentials:")
		noneMsg = T("未发现可列目录的桶（凭证可能无权限）", "no listable bucket found (credentials may lack permission)")
	}
	fmt.Println()
	if len(listable) > 0 {
		fmt.Printf("%s %s\n", cGreenBright("★"),
			cGreenBright(fmt.Sprintf(foundMsg, len(listable))))
		for _, r := range listable {
			fmt.Printf("  %s %s\n", cGreenBright("★"), r.URL)
			fmt.Printf("    %s %s\n", cDim(T("→ 可直接使用:", "→ ready to use:")),
				fmt.Sprintf("oss ls %q", r.URL+"?delimiter=/"))
		}
	} else {
		fmt.Printf("%s %s\n", cGrey("✗"), noneMsg)
	}
}

// checkMarkStdout returns a plain check mark for stdout messages.
func checkMarkStdout() string {
	if colorEnabled() {
		return "\x1b[32m✓\x1b[0m"
	}
	return "✓"
}
