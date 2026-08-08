package app

import (
	"context"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/urfave/cli/v3"

	"github.com/ejfkdev/oss/internal/s3x"
)

// RepoURL is the project homepage shown in help and version output.
const RepoURL = "https://github.com/ejfkdev/oss"

// New builds the root CLI command.
func New(version string) *cli.Command {
	// `oss --version`
	cli.VersionPrinter = func(cmd *cli.Command) {
		fmt.Printf("oss %s\n%s\n", cmd.Version, RepoURL)
	}
	// Root help uses a custom, dns-style rendered layout (bilingual);
	// subcommands keep the framework template with bilingual texts.
	cli.HelpPrinter = func(out io.Writer, templ string, data any) {
		if cmd, ok := data.(*cli.Command); ok && cmd.Root() == cmd {
			printRootHelp(out, version)
			return
		}
		cli.DefaultPrintHelp(out, templ, data)
	}

	return &cli.Command{
		Name:    "oss",
		Usage:   T("S3 协议跨云对象存储客户端", "S3-compatible object storage CLI"),
		Version: version,
		Action: func(ctx context.Context, c *cli.Command) error {
			// Bare `oss` (no command) prints the help.
			return cli.ShowRootCommandHelp(c)
		},
		Commands: []*cli.Command{
			lsCmd(),
			catCmd(),
			statCmd(),
			cpCmd(),
			presignCmd(),
		},
	}
}

// ---------------------------------------------------------------- root help

// renderRows renders "key -> description" rows with the key column padded to
// a common width (counted in runes; keys are ASCII so this aligns correctly)
// and optionally colored.
func renderRows(rows [][2]string, keyColor func(string) string, indent string) []string {
	maxw := 0
	for _, r := range rows {
		if w := utf8.RuneCountInString(r[0]); w > maxw {
			maxw = w
		}
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		pad := maxw - utf8.RuneCountInString(r[0])
		k := r[0]
		if keyColor != nil {
			k = keyColor(k)
		}
		out = append(out, indent+k+strings.Repeat(" ", pad)+"  "+r[1])
	}
	return out
}

func printSection(b *strings.Builder, title string, lines []string) {
	fmt.Fprintf(b, "\n%s:\n", cBold(title))
	for _, l := range lines {
		fmt.Fprintln(b, l)
	}
}

// printRootHelp renders the dns-style root help in the detected language.
func printRootHelp(w io.Writer, version string) {
	var b strings.Builder

	fmt.Fprintf(&b, "%s v%s — %s\n",
		cBold("oss"), version,
		T("S3 协议跨云对象存储命令行工具（AWS / 阿里云 / 腾讯云 / 华为云 / 七牛 / GCS / R2 / MinIO …）",
			"S3-compatible object storage CLI (AWS / Aliyun / Tencent / Huawei / Qiniu / GCS / R2 / MinIO ...)"))
	fmt.Fprintf(&b, "%s: %s\n", T("仓库", "Repo"), RepoURL)

	fmt.Fprintf(&b, "\n%s:  %s\n", cBold(T("用法", "USAGE")),
		"oss <command> [options] [target]")

	printSection(&b, T("命令", "COMMANDS"), renderRows([][2]string{
		{"ls", T("列出桶或对象（只看不传）：过滤 / 分页 / 缓存 / 导出列表", "List buckets or objects (read-only): filter / paging / cache / export the list")},
		{"cat", T("输出对象内容到 stdout（支持 --range）", "Print object content to stdout (supports --range)")},
		{"stat", T("查看桶或对象的元数据", "Show bucket or object metadata")},
		{"cp", T("传输文件：下载 / 上传 / 跨桶拷贝（并行分片、过滤条件）", "Transfer files: download / upload / cross-bucket copy (parallel multipart, filters)")},
		{"presign", T("生成对象的预签名 URL", "Generate a pre-signed URL for an object")},
	}, cCyan, "  "))

	printSection(&b, T("常用示例", "EXAMPLES"), renderRows([][2]string{
		{`oss ls https://bucket.s3.us-east-1.amazonaws.com/?delimiter=/`, T("匿名浏览公共桶（URL 即入口）", "Browse a public bucket anonymously (URL is the entry)")},
		{`oss ls oss://mybucket/logs/ --provider aliyun --ak <AK> --sk <SK>`, T("阿里云 OSS", "Aliyun OSS")},
		{`oss ls s3://mybucket -d`, T("只列目录（PRE=目录/公共前缀）", "List directories only (PRE = common prefix)")},
		{`oss ls s3://mybucket/logs/ -f --include "*.gz" -n 100`, T("只列文件 + glob 过滤 + 限 100 条", "Files only + glob filter + limit 100")},
		{`oss ls s3://mybucket --export list.xlsx`, T("导出文件列表（txt/csv/xlsx/yaml/md）", "Export the file list (txt/csv/xlsx/yaml/md)")},
		{`oss cp s3://mybucket/path/file.tar.gz .`, T("下载单个文件", "Download a single file")},
		{`oss cp -r s3://mybucket/logs/ ./logs --include "*.gz"`, T("按条件批量下载（保持目录结构）", "Filtered batch download (keeps directory layout)")},
		{`oss cp ./dist s3://mybucket/release/ -r`, T("上传目录", "Upload a directory")},
		{`oss cat s3://mybucket/config.yaml --range 0-1023`, T("查看对象片段", "View a byte range of an object")},
		{`oss presign s3://mybucket/file.tar.gz --expires 1h`, T("生成 1 小时有效的预签名链接", "Generate a presigned URL valid for 1h")},
	}, cCyan, "  "))

	printSection(&b, T("目标写法", "TARGET SYNTAX"), renderRows([][2]string{
		{"s3://bucket/prefix/", T("scheme 写法（也支持 oss:// cos:// obs://）", "scheme style (also oss:// cos:// obs://)")},
		{"mybucket/prefix/", T("裸桶名（配合 --provider / -e / 环境变量）", "bare bucket (with --provider / -e / env vars)")},
		{"https://bucket.s3.region.amazonaws.com/key?prefix=x/", T("完整 URL；查询参数即过滤条件", "full URL; query params act as filters")},
		{"https://host/bucket/prefix/", T("目录转发桶 / MinIO（自动 path-style）", "path-forwarded bucket / MinIO (auto path-style)")},
		{"http://host/bucket?token=abc", T("额外参数透传到每个请求", "extra params passed through to every request")},
	}, nil, "  "))

	printSection(&b, T("凭证解析顺序", "CREDENTIAL RESOLUTION"), []string{
		"  --ak/--sk/--token(STS)  >  OSS_* " + T("环境变量", "env") + "  >  AWS_* " + T("环境变量", "env"),
		"  >  ~/.aws profile" + T("（支持 assume-role）", " (assume-role supported)") + "  >  " + T("匿名", "anonymous"),
	})

	printSection(&b, T("公共选项（所有子命令通用）", "COMMON OPTIONS (shared by all subcommands)"), renderRows([][2]string{
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
	}, nil, "  "))

	printSection(&b, T("全局选项", "GLOBAL OPTIONS"), renderRows([][2]string{
		{"-h, --help", T("显示帮助", "show help")},
		{"-v, --version", T("显示版本", "show version")},
	}, nil, "  "))

	fmt.Fprintf(&b, "\n%s\n%s\n",
		T("职责分工: ls 负责\"看\"（列举/导出列表），cp 负责\"传\"（下载/上传/拷贝）",
			"Division of labor: ls is for viewing (list/export), cp is for transferring (download/upload/copy)"),
		T("各命令完整参数: oss <command> -h", "Full options per command: oss <command> -h"))

	fmt.Fprint(w, b.String())
}

// -------------------------------------------------------------- conn flags

// connFlags returns the connection flags shared by every subcommand.
func connFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "ak", Usage: T("AccessKey ID（环境变量 OSS_ACCESS_KEY_ID / AWS_ACCESS_KEY_ID）", "AccessKey ID (env: OSS_ACCESS_KEY_ID / AWS_ACCESS_KEY_ID)")},
		&cli.StringFlag{Name: "sk", Usage: T("AccessKey Secret（环境变量 OSS_SECRET_ACCESS_KEY / AWS_SECRET_ACCESS_KEY）", "AccessKey Secret (env: OSS_SECRET_ACCESS_KEY / AWS_SECRET_ACCESS_KEY)")},
		&cli.StringFlag{Name: "token", Usage: T("STS 会话令牌（环境变量 OSS_SESSION_TOKEN / AWS_SESSION_TOKEN）", "STS session token (env: OSS_SESSION_TOKEN / AWS_SESSION_TOKEN)")},
		&cli.StringFlag{Name: "profile", Usage: T("AWS 共享配置 profile（~/.aws/config，支持 assume-role）", "AWS shared config profile (~/.aws/config, assume-role supported)")},
		&cli.BoolFlag{Name: "anonymous", Usage: T("强制匿名访问", "force anonymous access")},
		&cli.StringFlag{Name: "provider", Usage: T("云厂商", "storage provider") + ": " + strings.Join(s3x.ProviderNames(), "|")},
		&cli.StringFlag{Name: "endpoint", Aliases: []string{"e"}, Usage: T("自定义 endpoint（覆盖厂商默认值）", "custom endpoint (overrides provider default)")},
		&cli.StringFlag{Name: "region", Usage: T("区域（覆盖 URL 推导值）", "region (overrides the URL-derived one)")},
		&cli.BoolFlag{Name: "path-style", Usage: T("强制 path-style 寻址（http://host/bucket/key）", "force path-style addressing (http://host/bucket/key)")},
		&cli.StringFlag{Name: "bucket", Usage: T("显式指定桶名（URL 无法识别时）", "explicit bucket name (when the URL is ambiguous)")},
		&cli.StringFlag{Name: "proxy", Aliases: []string{"x"}, Usage: T("HTTP 代理，如 http://127.0.0.1:7890", "HTTP proxy, e.g. http://127.0.0.1:7890")},
		&cli.StringSliceFlag{Name: "header", Aliases: []string{"H"}, Usage: T("附加 HTTP 头 'Key: Value'，可重复（User-Agent、Cookie 等）", "extra HTTP header 'Key: Value', repeatable (User-Agent, Cookie, ...)")},
		&cli.BoolFlag{Name: "insecure", Aliases: []string{"k"}, Usage: T("跳过 TLS 证书校验", "skip TLS certificate verification")},
		&cli.DurationFlag{Name: "timeout", Usage: T("单请求超时，如 30s（0 = 不超时）", "per-request timeout, e.g. 30s (0 = none)")},
		&cli.StringFlag{Name: "color", Value: "auto", Usage: T("彩色输出: auto|always|never（auto=仅交互式终端）", "color output: auto|always|never (auto = interactive terminals only)")},
	}
}

// connOpts extracts ConnOpts from the command flags.
func connOpts(c *cli.Command) *s3x.ConnOpts {
	// Apply the --color preference before anything is rendered.
	setColorPreference(parseColorPref(c.String("color")))
	return &s3x.ConnOpts{
		Provider:  c.String("provider"),
		Endpoint:  c.String("endpoint"),
		Region:    c.String("region"),
		Bucket:    c.String("bucket"),
		PathStyle: c.Bool("path-style"),
		AK:        c.String("ak"),
		SK:        c.String("sk"),
		Token:     c.String("token"),
		Profile:   c.String("profile"),
		Anonymous: c.Bool("anonymous"),
		Proxy:     c.String("proxy"),
		Headers:   c.StringSlice("header"),
		Insecure:  c.Bool("insecure"),
		Timeout:   c.Duration("timeout"),
	}
}
