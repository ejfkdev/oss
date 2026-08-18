package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

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
}

func findCmd() *cli.Command {
	flags := append([]cli.Flag{
		&cli.IntFlag{Name: "jobs", Value: 0, Usage: T("并发探测数（默认全部并发）", "concurrent probes (default: all at once)")},
		&cli.BoolFlag{Name: "cn", Usage: T("只探测中国大陆+港台地域（默认行为）", "probe only mainland-China + HK/TW regions (the default)")},
		&cli.BoolFlag{Name: "global", Usage: T("探测全部地域（含海外）", "probe all regions (including overseas)")},
		&cli.BoolFlag{Name: "listable", Aliases: []string{"l"}, Usage: T("仅输出可匿名列目录的桶：发现即流式打印完整访问 URL（配合 -j 输出 NDJSON），适合管道处理", "only output anonymously listable buckets: stream full access URLs as they are found (NDJSON with -j), pipe-friendly")},
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

探测方式：向桶发匿名 ListObjects 请求，一次请求同时判断
   存在性 + 能否匿名列目录：
   HTTP 200        → 存在且可匿名列目录（★ 亮绿高亮）
   HTTP 3xx        → 存在（重定向）
   HTTP 401/403    → 存在但私有（不可匿名列）
   HTTP 404/域名不存在 → 不存在
   超时/其它状态码   → 无法判断

示例:
   oss find mybucket                       查找单个桶名
   oss find bucket-a bucket-b bucket-c     批量（命令行多个）
   cat buckets.txt | oss find              批量（stdin 一行一个）
   oss find https://mybucket.s3.us-east-1.amazonaws.com/   完整 URL
   oss find mybucket -j                    NDJSON 输出
   oss find bucket-a bucket-b --export r.csv   导出 CSV（含 listable_url）
   oss find mybucket --listable            仅流式输出可匿名列目录桶的完整 URL
   oss find mybucket --listable -j         同上，NDJSON（发现即输出）
   cat buckets.txt | oss find --listable | xargs -I{} oss ls "{}"

说明:
   - 默认只探测中国大陆+港台地域（--cn 显式指定同效）；--global 探测全部地域（含海外）；
     --region 可只探测指定区域。未找到不代表绝对不存在，可用 --region 或 --global 重试
   - 默认只显示命中的厂商（未发现/无法判断的探测不再逐条列出）；
     --listable 只流式输出可匿名列桶的完整访问地址，stdout 无其它内容，便于管道处理
   - 腾讯云桶名需含 APPID 后缀（如 mybucket-1250000000）
   - 不支持七牛：匿名访问一律返回 400，无法判断存在性；B2 恒返回 403、R2 需账号 ID，
     也都不在探测范围
   - 探测为匿名请求，不发送任何凭证`,
			`oss find <bucket|URL> [...]  or  cat list.txt | oss find

Batch: give multiple arguments, and/or pipe one entry per line via stdin
(both can be combined). Each input can be:
   - a bucket name (e.g. mybucket) -> probe all known providers' regions
   - a full bucket URL/path (e.g. https://mybucket.s3.us-east-1.amazonaws.com/prefix
     or s3://mybucket/key) -> probe only that endpoint (more precise)

Probing: an anonymous ListObjects request per bucket reveals both existence
and anonymous-listability in a single request:
   HTTP 200        -> exists AND anonymously listable (bright green)
   HTTP 3xx        -> exists (redirect)
   HTTP 401/403    -> exists but private (not anonymously listable)
   HTTP 404 / no such host -> not found
   timeout / other status  -> inconclusive

EXAMPLES:
   oss find mybucket                       find a single bucket name
   oss find bucket-a bucket-b bucket-c     batch (multiple arguments)
   cat buckets.txt | oss find              batch (stdin, one per line)
   oss find https://mybucket.s3.us-east-1.amazonaws.com/   full URL
   oss find mybucket -j                    NDJSON output
   oss find bucket-a bucket-b --export r.csv   export CSV (with listable_url)
   oss find mybucket --listable            stream only full URLs of anonymously listable buckets
   oss find mybucket --listable -j         same, as NDJSON (emitted as found)
   cat buckets.txt | oss find --listable | xargs -I{} oss ls "{}"

NOTES:
   - By default only mainland-China + HK/TW regions are probed (--cn is the
     same); --global probes all regions (incl. overseas); --region probes a
     single region. "not found" is not a guarantee — retry with --region/--global
   - Only matched providers are shown by default (not-found/inconclusive probes
     are no longer listed); --listable streams only the full access URLs of
     anonymously listable buckets — nothing else goes to stdout, pipe-friendly
   - Tencent COS bucket names include the APPID suffix (e.g. mybucket-1250000000)
   - Qiniu is not supported (anonymous requests always return 400, so existence
     cannot be determined); B2 always returns 403 and R2 needs an account ID,
     so none of these are probed
   - Probes are anonymous; no credentials are ever sent`),
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
// every known provider; full URLs/paths probe only the parsed endpoint.
// global=true probes every region; otherwise only CN regions (mainland
// China + HK/TW) are probed. regionOverride, when set, takes precedence.
func buildFindJobs(inputs []string, o *s3x.ConnOpts, regionOverride string, global bool) (jobs []findResult, invalid map[string]string) {
	type job struct {
		input    string
		provider string
		name     string
		region   string
		url      string
	}
	var js []job
	invalid = map[string]string{}

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
			js = append(js, job{input: in, provider: prov, name: name, region: t.Region, url: root})
			continue
		}
		// Bare bucket name: fan out to all providers.
		if !s3x.ValidBucketName(t.Bucket) {
			invalid[in] = T("无效桶名", "invalid bucket name")
			continue
		}
		for _, p := range s3x.ScanProbes {
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
		hc.Timeout = 6 * time.Second
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	regionOverride := c.String("region")
	if c.Bool("cn") && c.Bool("global") {
		return errors.New(T("--cn 与 --global 不能同时使用", "--cn and --global are mutually exclusive"))
	}
	global := c.Bool("global") // default (and --cn): CN regions only

	results, invalid := buildFindJobs(inputs, o, regionOverride, global)
	if len(results) == 0 && len(invalid) == len(inputs) {
		return errors.New(T("没有可探测的输入", "no probeable inputs"))
	}

	// Run all probes concurrently.
	listableOnly := c.Bool("listable")
	jsonOut := c.Bool("json")
	exportPath := c.String("export")
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	if n := c.Int("jobs"); n > 0 {
		g.SetLimit(n)
	}
	for i := range results {
		i := i
		g.Go(func() error {
			r := &results[i]
			status, state, detail, listable := probeBucket(gctx, hc, r.URL)
			mu.Lock()
			r.Status, r.State, r.Detail, r.Listable = status, state, detail, listable
			// --listable streams hits the moment each probe resolves, so
			// consumers can pipe URLs without waiting for the whole scan.
			if listableOnly && listable && exportPath == "" {
				if jsonOut {
					line, _ := sonic.Marshal(map[string]any{
						"input": r.Input, "provider": r.Provider, "name": r.Name,
						"region": r.Region, "url": r.URL,
					})
					fmt.Println(string(line))
				} else {
					fmt.Println(r.URL)
				}
			}
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()

	// ---- Listable-only mode ----
	if listableOnly {
		var hits []findResult
		for _, r := range results {
			if r.Listable {
				hits = append(hits, r)
			}
		}
		if exportPath != "" {
			if err := exportFindResults(exportPath, inputs, hits, invalid); err != nil {
				return err
			}
			fmt.Printf("%s %s\n", checkMarkStdout(),
				fmt.Sprintf(T("已导出 %d 个可匿名列目录的桶 → %s", "exported %d anonymously listable bucket(s) → %s"), len(hits), exportPath))
			return nil
		}
		// Summary goes to stderr so stdout stays clean for piping.
		if len(hits) == 0 {
			fmt.Fprintf(os.Stderr, "%s %s\n", cGrey("✗"),
				cGrey(T("未发现可匿名列目录的桶", "no anonymously listable bucket found")))
		} else {
			fmt.Fprintf(os.Stderr, "%s %s\n", cGreenBright("★"),
				cGreenBright(fmt.Sprintf(T("共发现 %d 个可匿名列目录的桶", "found %d anonymously listable bucket(s) in total"), len(hits))))
		}
		return nil
	}

	// ---- Export mode ----
	if path := c.String("export"); path != "" {
		if err := exportFindResults(path, inputs, results, invalid); err != nil {
			return err
		}
		fmt.Printf("%s %s\n", checkMarkStdout(),
			fmt.Sprintf(T("已导出 %d 条探测结果 → %s", "exported %d probe result(s) → %s"), len(results), path))
		return nil
	}

	// ---- JSON mode ----
	if c.Bool("json") {
		for i := range results {
			line, _ := sonic.Marshal(&results[i])
			fmt.Println(string(line))
		}
		// Summary line with the dedicated anonymously-listable URL list.
		listable := make([]map[string]any, 0)
		found := 0
		for i := range results {
			r := &results[i]
			if r.State == findListable || r.State == findExists {
				found++
			}
			if r.Listable {
				listable = append(listable, map[string]any{
					"input": r.Input, "provider": r.Provider, "name": r.Name,
					"region": r.Region, "url": r.URL,
				})
			}
		}
		line, _ := sonic.Marshal(map[string]any{
			"inputs":             len(inputs),
			"found":              found,
			"anonymous_listable": listable,
		})
		fmt.Println(string(line))
		return nil
	}

	// ---- Human-readable output ----
	printFindResults(inputs, results, invalid)
	return nil
}

// stateStyle returns (marker, colored-state-text) for a result state.
func stateStyle(state string) (string, string) {
	switch state {
	case findListable:
		return cGreenBright("★"), cGreenBright(T("可匿名列目录", "listable"))
	case findExists:
		return cYellow("✓"), cYellow(T("存在·私有", "exists·private"))
	case findNotFound:
		return cGrey("✗"), cGrey(T("未发现", "not found"))
	default:
		return cGrey("?"), cGrey(T("无法判断", "unknown"))
	}
}

// printFindResults renders grouped, color-highlighted results.
func printFindResults(inputs []string, results []findResult, invalid map[string]string) {
	// Group by input, preserving input order.
	byInput := make(map[string][]*findResult)
	for i := range results {
		r := &results[i]
		byInput[r.Input] = append(byInput[r.Input], r)
	}

	multi := len(inputs) > 1
	w := tabwriter.NewWriter(os.Stdout, 1, 2, 2, ' ', 0)

	for _, in := range inputs {
		if msg, bad := invalid[in]; bad {
			if multi {
				fmt.Fprintf(w, "%s\t%s\t%s\n", cGrey("·"), in, cGrey(msg))
			} else {
				fmt.Fprintf(w, "%s\t%s\n", cGrey("·"), msg)
			}
			continue
		}
		rs := byInput[in]
		if multi {
			fmt.Fprintf(w, "%s\n", cBold(in))
		}
		// Only matched providers are shown (listable > exists); not-found and
		// inconclusive probes stay hidden to keep the output uncluttered.
		var foundList []*findResult
		for _, r := range rs {
			if r.State == findListable || r.State == findExists {
				foundList = append(foundList, r)
			}
		}
		sort.SliceStable(foundList, func(a, b int) bool {
			if (foundList[a].State == findListable) != (foundList[b].State == findListable) {
				return foundList[a].State == findListable
			}
			return false
		})
		for _, r := range foundList {
			mark, stateText := stateStyle(r.State)
			region := ""
			if r.Region != "" {
				region = " @ " + r.Region
			}
			fmt.Fprintf(w, "  %s\t%s%s\t%s\t%s\n",
				mark, r.Name, region, stateText, r.URL)
		}
		if len(foundList) == 0 {
			fmt.Fprintf(w, "  %s\t%s\n", cGrey("✗"),
				cGrey(T("未在已知厂商中发现（可尝试 --global / --region 扩大范围）", "not found on known providers (try --global / --region)")))
		}
	}
	_ = w.Flush()

	// Summary of anonymously listable buckets (the highlight).
	var listable []*findResult
	for i := range results {
		if results[i].Listable {
			listable = append(listable, &results[i])
		}
	}
	fmt.Println()
	if len(listable) > 0 {
		fmt.Printf("%s %s\n", cGreenBright("★"),
			cGreenBright(fmt.Sprintf(T("发现 %d 个可匿名列目录的桶:", "found %d anonymously listable bucket(s):"), len(listable))))
		for _, r := range listable {
			fmt.Printf("  %s %s\n", cGreenBright("★"), r.URL)
			fmt.Printf("    %s %s\n", cDim(T("→ 可直接使用:", "→ ready to use:")),
				fmt.Sprintf("oss ls %q", r.URL+"?delimiter=/"))
		}
	} else {
		fmt.Printf("%s %s\n", cGrey("✗"),
			T("未发现可匿名列目录的桶", "no anonymously listable bucket found"))
	}
}

// checkMarkStdout returns a plain check mark for stdout messages.
func checkMarkStdout() string {
	if colorEnabled() {
		return "\x1b[32m✓\x1b[0m"
	}
	return "✓"
}
