package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/bytedance/sonic"
	"github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"

	"github.com/ejfkdev/oss/internal/s3x"
)

// Bucket-existence states derived from probe responses. This is a
// best-effort OSINT technique: providers answer differently, so results are
// indications, not guarantees.
const (
	scanExists   = "exists"   // bucket exists (public or private)
	scanNotFound = "notfound" // 404 / NXDOMAIN: bucket does not exist
	scanUnknown  = "unknown"   // timeout / unexpected status: inconclusive
)

type probeResult struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Region   string `json:"region,omitempty"`
	URL      string `json:"url"`
	Status   int    `json:"status,omitempty"`
	State    string `json:"state"` // exists | notfound | unknown
	Detail   string `json:"detail"`
}

func scanCmd() *cli.Command {
	flags := append([]cli.Flag{
		&cli.IntFlag{Name: "jobs", Value: 0, Usage: T("并发探测数（默认全部并发）", "concurrent probes (default: all at once)")},
		&cli.BoolFlag{Name: "json", Aliases: []string{"j"}, Usage: T("NDJSON 输出（每个厂商一行）", "NDJSON output (one line per provider)")},
	}, connFlags()...)
	return &cli.Command{
		Name:    "scan",
		Aliases: []string{"which"},
		Usage:   T("扫描桶名存在于哪些云存储服务", "scan which cloud storage services host a bucket name"),
		UsageText: T(`oss scan <桶名> [--region <区域>]

对每个已知云存储厂商构造桶 URL 并匿名探测，根据响应判断桶是否存在：
   HTTP 200/204     → 存在（公开可读）
   HTTP 3xx         → 存在（重定向，可能在其他区域）
   HTTP 401/403     → 存在（私有，拒绝匿名访问）
   HTTP 404 / 域名解析失败 → 不存在
   超时 / 其它状态码 → 无法判断

示例:
   oss scan mybucket                    扫描所有厂商的常用区域
   oss scan mybucket --region cn-beijing 只探测指定区域
   oss scan mybucket -j                 NDJSON 输出

说明:
   - 各厂商只探测内置的常用区域（AWS/GCS 为全域探测）；
     未找到不代表绝对不存在，可用 --region 指定区域重试
   - 腾讯云桶名需含 APPID 后缀（如 mybucket-1250000000）
   - B2、七牛不支持匿名探测（恒返回错误），R2 需账号 ID，均不在探测范围
   - 探测为匿名请求，不发送任何凭证`,
			`oss scan <bucket-name> [--region <region>]

Probes every known cloud provider with the constructed bucket URL and
interprets the anonymous response:
   HTTP 200/204     -> exists (public)
   HTTP 3xx         -> exists (redirect, may live in another region)
   HTTP 401/403     -> exists (private, anonymous access denied)
   HTTP 404 / DNS failure -> not found
   timeout / other status -> inconclusive

EXAMPLES:
   oss scan mybucket                      scan the default regions of all providers
   oss scan mybucket --region cn-beijing  probe only the given region
   oss scan mybucket -j                   NDJSON output

NOTES:
   - Only built-in common regions are probed (AWS/GCS are probed globally);
     "not found" is not a guarantee — retry with --region when in doubt
   - Tencent COS bucket names include the APPID suffix (e.g. mybucket-1250000000)
   - B2 and Qiniu cannot be probed anonymously (they always error); R2 needs an
     account ID — none of these are probed
   - Probes are anonymous; no credentials are ever sent`),
		Flags:  flags,
		Action: runScan,
	}
}

func runScan(ctx context.Context, c *cli.Command) error {
	o := connOpts(c)
	bucket := strings.TrimSpace(c.Args().First())
	if bucket == "" {
		return errors.New(T("用法: oss scan <桶名>", "usage: oss scan <bucket-name>"))
	}
	if !s3x.ValidBucketName(bucket) {
		return fmt.Errorf(T("无效的桶名 %q", "invalid bucket name %q"), bucket)
	}

	// Probe timeout: honor --timeout, default to 6s so dead hosts cannot
	// stall the whole scan.
	hc := s3x.NewHTTPClient(o)
	if hc.Timeout == 0 {
		hc.Timeout = 6 * time.Second
	}
	// A redirect already proves existence; do not follow it.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	regionOverride := c.String("region")

	type job struct {
		probe  s3x.ScanProbe
		region string
		url    string
	}
	var jobs []job
	for _, p := range s3x.ScanProbes {
		urls := p.ScanURLs(bucket, regionOverride)
		if len(p.Regions) == 0 {
			jobs = append(jobs, job{probe: p, url: urls[0]})
			continue
		}
		regions := p.Regions
		if regionOverride != "" {
			regions = []string{regionOverride}
		}
		for i, u := range urls {
			jobs = append(jobs, job{probe: p, region: regions[i], url: u})
		}
	}

	var (
		mu      sync.Mutex
		results = make([]probeResult, 0, len(jobs))
	)
	g, gctx := errgroup.WithContext(ctx)
	if n := c.Int("jobs"); n > 0 {
		g.SetLimit(n)
	}
	for _, j := range jobs {
		j := j
		g.Go(func() error {
			status, state, detail := probeBucket(gctx, hc, j.url)
			mu.Lock()
			results = append(results, probeResult{
				Provider: j.probe.Provider, Name: j.probe.Name,
				Region: j.region, URL: j.url,
				Status: status, State: state, Detail: detail,
			})
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()

	// Aggregate per provider (in probe definition order).
	type verdict struct {
		probe   s3x.ScanProbe
		state   string
		matches []probeResult
		others  []probeResult
	}
	var verdicts []verdict
	for _, p := range s3x.ScanProbes {
		v := verdict{probe: p, state: scanNotFound}
		for _, r := range results {
			if r.Provider != p.Provider {
				continue
			}
			if r.State == scanExists {
				v.matches = append(v.matches, r)
			} else {
				v.others = append(v.others, r)
			}
		}
		if len(v.matches) > 0 {
			v.state = scanExists
		} else {
			for _, r := range v.others {
				if r.State == scanUnknown {
					v.state = scanUnknown
					break
				}
			}
		}
		verdicts = append(verdicts, v)
	}

	if c.Bool("json") {
		for _, v := range verdicts {
			line, _ := sonic.Marshal(map[string]any{
				"provider": v.probe.Provider,
				"name":     v.probe.Name,
				"state":    v.state,
				"matches":  v.matches,
				"probes":   v.others,
			})
			fmt.Println(string(line))
		}
		return nil
	}

	// Human-readable table.
	w := tabwriter.NewWriter(os.Stdout, 1, 2, 2, ' ', 0)
	var found []string
	for _, v := range verdicts {
		var mark, stateText, detail string
		switch v.state {
		case scanExists:
			mark = cGreen("✓")
			stateText = cGreen(T("存在", "exists"))
			detail = existsDetail(v.matches)
			found = append(found, fmt.Sprintf("%s (%s)", v.probe.Name, existsDetailShort(v.matches)))
		case scanUnknown:
			mark = cYellow("?")
			stateText = cYellow(T("无法判断", "unknown"))
			detail = unknownDetail(v.others)
		default:
			mark = cDim("✗")
			stateText = cDim(T("未发现", "not found"))
			detail = cDim(notFoundDetail(v.probe, regionOverride))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", mark, v.probe.Name, stateText, detail)
	}
	_ = w.Flush()

	fmt.Println()
	if len(found) > 0 {
		fmt.Printf("%s %s%s\n", cGreen("✓"),
			fmt.Sprintf(T("桶 %q 存在于 %d 个服务: ", "bucket %q found on %d service(s): "), bucket, len(found)),
			strings.Join(found, ", "))
	} else {
		fmt.Printf("%s %s\n", cDim("✗"),
			fmt.Sprintf(T("未在常用区域找到桶 %q；不代表绝对不存在，可用 --region 指定区域重试",
				"bucket %q not found in the common regions; not a guarantee — retry with --region"), bucket))
	}
	return nil
}

// probeBucket performs one anonymous GET and classifies the outcome.
func probeBucket(ctx context.Context, hc *http.Client, url string) (status int, state, detail string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, scanUnknown, err.Error()
	}
	resp, err := hc.Do(req)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			return 0, scanNotFound, T("域名不存在", "no such host")
		}
		if os.IsTimeout(err) || strings.Contains(err.Error(), "deadline") {
			return 0, scanUnknown, T("超时", "timeout")
		}
		return 0, scanUnknown, T("连接失败", "connection failed")
	}
	defer resp.Body.Close()
	_, _ = io.CopyN(io.Discard, resp.Body, 512)

	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent:
		return resp.StatusCode, scanExists, T("公开可读", "public")
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return resp.StatusCode, scanExists, T("重定向", "redirect")
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return resp.StatusCode, scanExists, T("私有", "private")
	case resp.StatusCode == http.StatusNotFound:
		return resp.StatusCode, scanNotFound, T("桶不存在", "no such bucket")
	default:
		return resp.StatusCode, scanUnknown, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
}

func existsDetail(matches []probeResult) string {
	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		s := fmt.Sprintf("%s (HTTP %d", m.Detail, m.Status)
		if m.Region != "" {
			s += " @ " + m.Region
		}
		parts = append(parts, s+")")
	}
	return strings.Join(parts, "; ")
}

func existsDetailShort(matches []probeResult) string {
	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		s := m.Detail
		if m.Region != "" {
			s += " @ " + m.Region
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

func unknownDetail(others []probeResult) string {
	parts := make([]string, 0, len(others))
	for _, r := range others {
		if r.State != scanUnknown {
			continue
		}
		s := r.Detail
		if r.Region != "" {
			s += " @ " + r.Region
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}

func notFoundDetail(p s3x.ScanProbe, regionOverride string) string {
	if len(p.Regions) == 0 || regionOverride != "" {
		return T("不存在", "not found")
	}
	return fmt.Sprintf(T("已探测 %d 个常用区域，均不存在", "probed %d common region(s), none matched"), len(p.Regions))
}
