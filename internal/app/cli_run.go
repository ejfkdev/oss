package app

// The xyz-go-backed CLI entry point. The urfave/cli frontend was migrated to
// xyz-go's CLI (v0.2.4) so that commands, REST and MCP share one definition.
//
// Behavioral compatibility with the previous CLI is the goal; every known
// deviation is documented in the release notes.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/ejfkdev/xyz-go/cli"
	errs "github.com/ejfkdev/xyz-go/errors"
	"github.com/ejfkdev/xyz-go/langx"
	"github.com/ejfkdev/xyz-go/registry"

	"github.com/ejfkdev/oss/internal/s3x"
)

const cliBin = "oss"

// Run builds the CLI registry, wires the middlewares that restore the
// previous CLI behavior (color preference, -j/--json flowing into handlers,
// error prefix) and dispatches args. It returns the process exit code.
func Run(version string, args []string) int {
	apiVersion = version

	// xyz-go v0.3.1+ renders its own interface strings (positional-count
	// errors, help labels, MCP surfaces) through the langx catalog; align it
	// with the oss language detection so those strings are bilingual too.
	if chineseEnv {
		langx.Set(langx.ZhCn, nil)
	} else {
		langx.Set(langx.En, nil)
	}

	reg, err := buildCLIRegistry()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	// Root-level help (bare invocation, `oss -h`): print the custom dns-style
	// bilingual overview, as before. Deeper "-h" stays with the frontend.
	if len(args) == 0 || (len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help")) {
		runOverview(reg)
		return 0
	}

	// Legacy spelling: the previous CLI accepted "-j" as a shorthand for
	// "--json". xyz-go's frontend only knows the long form (the flag is
	// handled before command parsing), so translate tokens ahead of dispatch.
	for i, a := range args {
		if a == "-j" {
			args[i] = "--json"
		}
	}

	app, err := cli.NewWithOptions(reg, cli.Options{Out: os.Stdout, ErrOut: &errorWriter{w: os.Stderr}})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	app.Use(cliColorMiddleware, cliOutputMiddleware)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.RunContext(ctx, args)
}

// errorWriter keeps the old look of runtime errors: the red "oss: " prefix
// on stderr (colors only when colored output is enabled).
type errorWriter struct{ w io.Writer }

func (e *errorWriter) Write(p []byte) (int, error) {
	if colorEnabled() {
		fmt.Fprintf(e.w, "\x1b[1;31moss:\x1b[0m ")
	} else {
		fmt.Fprint(e.w, "oss: ")
	}
	return e.w.Write(p)
}

// cliColorMiddleware applies the legacy --color preference before the handler
// runs: interactive TTY output is colored, piped output stays plain. The
// tag default "auto" is not yet applied here (decode happens at Invoke), so
// an absent value means auto as before.
func cliColorMiddleware(_ context.Context, _ *cli.ExecContext, args map[string]any, next func() error) error {
	if v, ok := args["color"].(string); ok && v != "" {
		setColorPreference(parseColorPref(v))
	}
	return next()
}

// cliOutputMiddleware restores the CLI's streaming contract: every command
// handler prints its own output (colored rows, NDJSON, progress...), so the
// framework rendering path is skipped entirely. The --json/-j flag flows into
// the handler the same way it reached the old command actions, and the CLI
// mark lets shared handlers branch between the command line and HTTP/MCP
// (json:"-" fields are injected under their Go name).
//
// Non-coded handler errors are wrapped as internal so runtime failures keep
// the pre-migration exit code (1); usage errors (unknown flags, positional
// counts) never reach this middleware and still exit 2, per xyz semantics.
func cliOutputMiddleware(_ context.Context, ec *cli.ExecContext, args map[string]any, _ func() error) error {
	args["CLI"] = true
	if ec.JSON {
		args["JSON"] = true
	}
	_, err := ec.Entry.Invoke(context.Background(), args)
	if err != nil {
		var ce *errs.CodedError
		if !errors.As(err, &ce) {
			err = errs.WrapMsg(errs.KindInternal, err, err.Error())
		}
	}
	return err
}

// buildCLIRegistry assembles the registry for the command line: the five
// shared commands (ls/stat/cat/presign/find) plus the CLI-only cp, serve and
// mcp. The API registry (HTTP/MCP) is a subset built by BuildAPIRegistry.
func buildCLIRegistry() (*registry.Registry, error) {
	reg := registry.New()
	for _, fn := range []func(*registry.Registry) error{
		registerApixLs, registerApixStat, registerApixCat, registerApixPresign, registerApixFind,
		registerCliCp, registerCliServe, registerCliMCP,
	} {
		if err := fn(reg); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

// runOverview renders the root help in the previous dns-style layout, sourced
// from the registry so the command table never drifts.
func runOverview(reg *registry.Registry) {
	var b strings.Builder
	bw := bufio.NewWriter(&b)

	version := apiVersion
	if version != "dev" {
		version = "v" + version
	}
	fmt.Fprintf(bw, "%s %s — %s\n", cBold(cliBin), version,
		T("S3 协议跨云对象存储命令行工具（AWS / 阿里云 / 腾讯云 / 华为云 / 百度 / 金山 / UCloud / 京东 / GCS / R2 / MinIO …）",
			"S3-compatible object storage CLI (AWS / Aliyun / Tencent / Huawei / Baidu / Kingsoft / UCloud / JD / GCS / R2 / MinIO ...)"))
	fmt.Fprintf(bw, "%s: %s\n", T("仓库", "Repo"), RepoURL)
	fmt.Fprintf(bw, "\n%s:  %s\n\n", cBold(T("用法", "USAGE")), cliBin+" <command> [options] [target]")

	// Command table straight from the registry (name + summary).
	names := make([]string, 0, len(reg.All()))
	byName := map[string]string{}
	for _, e := range reg.All() {
		names = append(names, e.Name)
		byName[e.Name] = e.Summary
	}
	sort.Strings(names)
	rows := make([][2]string, 0, len(names))
	for _, n := range names {
		rows = append(rows, [2]string{n, byName[n]})
	}
	printSection(bw, T("命令", "COMMANDS"), renderRows(rows, cCyan, "  "))

	printSection(bw, T("常用示例", "EXAMPLES"), renderRows([][2]string{
		{cliBin + ` ls https://bucket.s3.us-east-1.amazonaws.com/?delimiter=/`, T("匿名浏览公共桶（URL 即入口）", "Browse a public bucket anonymously (URL is the entry)")},
		{cliBin + ` ls oss://mybucket/logs/ --provider aliyun --ak <AK> --sk <SK>`, T("阿里云 OSS", "Aliyun OSS")},
		{cliBin + ` ls s3://mybucket -d`, T("只列目录（PRE=目录/公共前缀）", "List directories only (PRE = common prefix)")},
		{cliBin + ` ls s3://mybucket/logs/ -f --include "*.gz" -n 100`, T("只列文件 + glob 过滤 + 限 100 条", "Files only + glob filter + limit 100")},
		{cliBin + ` ls s3://mybucket --export list.xlsx`, T("导出文件列表（txt/csv/xlsx/yaml/md）", "Export the file list (txt/csv/xlsx/yaml/md)")},
		{cliBin + ` cp s3://mybucket/path/file.tar.gz .`, T("下载单个文件", "Download a single file")},
		{cliBin + ` cp -r s3://mybucket/logs/ ./logs --include "*.gz"`, T("按条件批量下载（保持目录结构）", "Filtered batch download (keeps directory layout)")},
		{cliBin + ` cp ./dist s3://mybucket/release/ -r`, T("上传目录", "Upload a directory")},
		{cliBin + ` cat s3://mybucket/config.yaml --range 0-1023`, T("查看对象片段", "View a byte range of an object")},
		{cliBin + ` presign s3://mybucket/file.tar.gz --expires 1h`, T("生成 1 小时有效的预签名链接", "Generate a presigned URL valid for 1h")},
		{cliBin + ` find mybucket --listable`, T("查找桶名并可匿名列目录（输出访问 URL）", "Discover a bucket and its anonymous listability (prints access URLs)")},
		{cliBin + ` serve --addr :8080 --bearer tok`, T("一键提供 REST / OpenAPI / MCP 服务", "Serve REST + OpenAPI + MCP on one port")},
		{cliBin + ` mcp stdio`, T("MCP 工具服务器（供 Claude 等客户端调用）", "MCP tool server (for Claude and other clients)")},
	}, cCyan, "  "))

	printSection(bw, T("目标写法", "TARGET SYNTAX"), renderRows([][2]string{
		{"s3://bucket/prefix/", T("scheme 写法（也支持 oss:// cos:// obs://）", "scheme style (also oss:// cos:// obs://)")},
		{"mybucket/prefix/", T("裸桶名（配合 --provider / -e / 环境变量）", "bare bucket (with --provider / -e / env vars)")},
		{"https://bucket.s3.region.amazonaws.com/key?prefix=x/", T("完整 URL；查询参数即过滤条件", "full URL; query params act as filters")},
		{"https://host/bucket/prefix/", T("目录转发桶 / MinIO（自动 path-style）", "path-forwarded bucket / MinIO (auto path-style)")},
		{"http://host/bucket?token=abc", T("额外参数透传到每个请求", "extra params passed through to every request")},
	}, nil, "  "))

	printSection(bw, T("凭证解析顺序", "CREDENTIAL RESOLUTION"), []string{
		"  --ak/--sk/--token(STS)  >  OSS_* " + T("环境变量", "env") + "  >  AWS_* " + T("环境变量", "env"),
		"  >  ~/.aws profile" + T("（支持 assume-role）", " (assume-role supported)") + "  >  " + T("匿名", "anonymous"),
	})

	printSection(bw, T("公共选项（所有子命令通用）", "COMMON OPTIONS (shared by all subcommands)"), renderRows([][2]string{
		{"--ak <ID> / --sk <SECRET>", T("静态凭证 AccessKey", "static AccessKey credentials")},
		{"--token <TOKEN>", T("STS 会话令牌", "STS session token")},
		{"--profile <NAME>", T("AWS 共享配置 profile（支持 assume-role）", "AWS shared config profile (assume-role supported)")},
		{"--anonymous", T("强制匿名访问", "force anonymous access")},
		{"--provider <NAME>", T("云厂商", "storage provider") + ": " + strings.Join(s3x.ProviderNames(), "|")},
		{"-e, --endpoint <URL>", T("自定义 endpoint（覆盖厂商默认值）", "custom endpoint (overrides provider default)")},
		{"--region <REGION>", T("区域（覆盖 URL 推导值）", "region (overrides the URL-derived one)")},
		{"--path-style", T("强制 path-style 寻址", "force path-style addressing")},
		{"--bucket <NAME>", T("显式指定桶名（URL 无法识别时）", "explicit bucket name (when the URL is ambiguous)")},
		{"-x, --proxy <URL>", T("HTTP 代理，如 http://127.0.0.1:7890", "HTTP proxy, e.g. http://127.0.0.1:7890")},
		{"-H, --header \"Key: Value\"", T("附加 HTTP 头，可重复（UA / Cookie 等）", "extra HTTP header, repeatable (UA / Cookie / ...)")},
		{"-k, --insecure", T("跳过 TLS 证书校验", "skip TLS certificate verification")},
		{"--timeout <DUR>", T("单请求超时，如 30s（0 = 不超时）", "per-request timeout, e.g. 30s (0 = none)")},
		{"--color <WHEN>", T("彩色输出 auto|always|never（auto = 仅交互终端）", "color output auto|always|never (auto = interactive only)")},
		{"--json / -j", T("JSON 输出（-j 为兼容旧写法，含义与 --json 相同）", "JSON output (-j is a compatibility spelling for --json)")},
	}, nil, "  "))

	printSection(bw, T("全局选项", "GLOBAL OPTIONS"), renderRows([][2]string{
		{"-h, --help", T("显示帮助", "show help")},
		{"-v, --version", T("显示版本", "show version")},
		{"completion <shell>", T("生成 shell 补全脚本", "generate a shell completion script")},
	}, nil, "  "))

	fmt.Fprintf(bw, "\n%s\n%s\n",
		T("职责分工: ls 负责\"看\"（列举/导出列表），cp 负责\"传\"（下载/上传/拷贝）",
			"Division of labor: ls is for viewing (list/export), cp is for transferring (download/upload/copy)"),
		T("各命令完整参数与示例: oss <命令> -h", "Full flags and examples per command: oss <command> -h"))

	bw.Flush()
	fmt.Fprint(os.Stdout, b.String())
}
