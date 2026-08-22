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

	"encoding/json"
	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/mattn/go-isatty"
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

// collectFindInputs gathers inputs from positional args and stdin (one per
// line), deduped.
func collectFindInputs(args []string) []string {
	inputs := append([]string{}, args...)
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

// FindOptions parametrize a find run beyond the connection options.
type FindOptions struct {
	Jobs     int
	Region   string
	Global   bool
	Cn       bool
	Listable bool
}

// findListableURL is one listable bucket in the report.
type findListableURL struct {
	Input    string `json:"input"`
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Region   string `json:"region,omitempty"`
	URL      string `json:"url"`
}

// FindReport is the structured outcome of a find run — the shape served by
// the HTTP/MCP interfaces; the CLI renders from it (streaming via onHit).
type FindReport struct {
	Auth              string            `json:"auth"` // anonymous | signed
	Inputs            int               `json:"inputs"`
	Found             int               `json:"found"`
	Results           []findResult      `json:"results"`
	Invalid           map[string]string `json:"invalid,omitempty"`
	AnonymousListable []findListableURL `json:"anonymous_listable"`
}

// findTargets performs the whole find pipeline: input validation, credential
// resolution, probe fan-out and aggregation. The callback hooks let the CLI
// keep its streaming behavior: onInvalid fires for unparseable inputs before
// probing, onSigned once when credentialed probing is active, and onHit for
// every mode-relevant hit the moment its probe resolves (called from probe
// goroutines; callers must synchronize their own output).
func findTargets(ctx context.Context, o *s3x.ConnOpts, inputs []string, opt FindOptions,
	onInvalid func(in, msg string), onSigned func(), onHit func(r *findResult, signed bool)) (*FindReport, error) {
	if len(inputs) == 0 {
		return nil, errors.New(T(
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
	if opt.Cn && opt.Global {
		return nil, errors.New(T("--cn 与 --global 不能同时使用", "--cn and --global are mutually exclusive"))
	}
	onlyProvider := strings.ToLower(strings.TrimSpace(o.Provider))
	if onlyProvider != "" {
		if _, ok := s3x.Providers[onlyProvider]; !ok {
			return nil, fmt.Errorf("%s (choose from: %s)",
				fmt.Sprintf(T("未知厂商 %q", "unknown provider %q"), onlyProvider),
				strings.Join(s3x.ProviderNames(), ", "))
		}
	}

	// Resolve credentials once. find probes anonymously by default; when
	// credentials are configured (--ak/--sk/--token, OSS_*/AWS_* env vars or
	// --profile) it switches to SigV4-signed probes, which can also confirm
	// non-anonymous buckets. --anonymous forces anonymous probing.
	credProvider, anon, err := s3x.ResolveCredentials(ctx, o, opt.Region)
	if err != nil {
		return nil, err
	}
	var creds aws.Credentials
	if !anon {
		creds, err = credProvider.Retrieve(ctx)
		if err != nil {
			return nil, fmt.Errorf(T("无法获取凭证: %v", "failed to retrieve credentials: %v"), err)
		}
		if onSigned != nil {
			onSigned()
		}
	}

	results, invalid := buildFindJobs(inputs, o, opt.Region, opt.Global)
	if len(results) == 0 && len(invalid) == len(inputs) {
		return nil, errors.New(T("没有可探测的输入", "no probeable inputs"))
	}
	if len(results) == 0 && onlyProvider != "" {
		return nil, errors.New(fmt.Sprintf(T("厂商 %q 没有可探测的端点", "provider %q has no probeable endpoints"), onlyProvider))
	}
	if onInvalid != nil {
		for _, in := range inputs {
			if msg, bad := invalid[in]; bad {
				onInvalid(in, msg)
			}
		}
	}

	// Run all probes concurrently. Two modes: default finds bucket storage
	// (exists OR listable hits); --listable finds only listable buckets.
	isHit := func(state string) bool {
		if opt.Listable {
			return state == findListable
		}
		return state == findListable || state == findExists
	}
	g, gctx := errgroup.WithContext(ctx)
	if opt.Jobs > 0 {
		g.SetLimit(opt.Jobs)
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
			r.Status, r.State, r.Detail, r.Listable = status, state, detail, listable
			if onHit != nil && isHit(state) {
				onHit(r, !anon)
			}
			return nil
		})
	}
	_ = g.Wait()

	report := &FindReport{Inputs: len(inputs), Results: results, Invalid: invalid, Auth: "anonymous"}
	if !anon {
		report.Auth = "signed"
	}
	for i := range results {
		r := &results[i]
		if isHit(r.State) {
			report.Found++
		}
		if r.Listable {
			report.AnonymousListable = append(report.AnonymousListable, findListableURL{
				Input: r.Input, Provider: r.Provider, Name: r.Name, Region: r.Region, URL: r.URL,
			})
		}
	}
	return report, nil
}

// findCLI is the command-line behavior of find: streaming hits, export and
// NDJSON — the same UX as before the CLI migration.
func findCLI(ctx context.Context, rawInputs []string, o *s3x.ConnOpts, opt FindOptions, jsonOut bool, exportPath string) error {
	inputs := collectFindInputs(rawInputs)
	if len(inputs) == 0 {
		return errors.New(T(
			"用法: oss find <桶名|URL> [...]（或 cat list.txt | oss find）",
			"usage: oss find <bucket|URL> [...] (or: cat list.txt | oss find)"))
	}

	// ---- Output wiring: human mode streams hits as they resolve ----
	human := !jsonOut && exportPath == ""
	multi := len(inputs) > 1
	var (
		w  *tabwriter.Writer
		mu sync.Mutex
	)
	onInvalid := func(in, msg string) {
		if !human {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if multi {
			fmt.Fprintf(w, "%s\t%s\t%s\n", cGrey("·"), in, cGrey(msg))
		} else {
			fmt.Fprintf(w, "%s\t%s\n", cGrey("·"), msg)
		}
		_ = w.Flush()
	}
	onSigned := func() {
		if human {
			fmt.Fprintln(os.Stderr, cDim(T(
				"使用提供的凭证进行签名探测（可验证非匿名桶）",
				"signed probing with the provided credentials (can verify non-anonymous buckets)")))
		}
	}
	onHit := func(r *findResult, signed bool) {
		if !human {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		mark, stateText := stateStyle(r.State, signed)
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
	if human {
		w = tabwriter.NewWriter(os.Stdout, 1, 2, 2, ' ', 0)
	}

	report, err := findTargets(ctx, o, inputs, opt, onInvalid, onSigned, onHit)
	if err != nil {
		return err
	}
	if human {
		_ = w.Flush()
	}

	// ---- Export mode ----
	if exportPath != "" {
		out := report.Results
		if opt.Listable {
			out = nil
			for _, r := range report.Results {
				if r.Listable {
					out = append(out, r)
				}
			}
		}
		if err := exportFindResults(exportPath, inputs, out, report.Invalid); err != nil {
			return err
		}
		fmt.Printf("%s %s\n", checkMarkStdout(),
			fmt.Sprintf(T("已导出 %d 条探测结果 → %s", "exported %d probe result(s) → %s"), len(out), exportPath))
		return nil
	}

	// ---- JSON mode ----
	if jsonOut {
		for i := range report.Results {
			if opt.Listable && !report.Results[i].Listable {
				continue
			}
			line, _ := json.Marshal(&report.Results[i])
			fmt.Println(string(line))
		}
		listable := make([]map[string]any, 0, len(report.AnonymousListable))
		for _, l := range report.AnonymousListable {
			listable = append(listable, map[string]any{
				"input": l.Input, "provider": l.Provider, "name": l.Name,
				"region": l.Region, "url": l.URL,
			})
		}
		line, _ := json.Marshal(map[string]any{
			"auth":               report.Auth,
			"inputs":             report.Inputs,
			"found":              report.Found,
			"anonymous_listable": listable,
		})
		fmt.Println(string(line))
		return nil
	}

	// ---- Human-readable tail ----
	// Matches already streamed during probing; print invalid/no-hit inputs
	// and the anonymously-listable summary.
	printFindTail(inputs, report.Results, report.Invalid, opt.Listable, report.Auth == "signed")
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
