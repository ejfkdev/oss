package app

import (
	"context"
	"time"

	errs "github.com/ejfkdev/xyz-go/errors"
	"github.com/ejfkdev/xyz-go/registry"
	"github.com/ejfkdev/xyz-go/spec"
)

// apixFindArgs follows the canonical connection fields documented in apix.go.
type apixFindArgs struct {
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

	Input    string   `json:"input,omitempty" desc:"要探测的桶名或完整 URL（CLI 第一个位置参数；HTTP/MCP 用重复的 inputs）/ bucket name or full URL to probe" cli:"positional"`
	Inputs   []string `json:"inputs,omitempty" desc:"追加的探测输入（可重复，HTTP 查询 inputs=a&inputs=b）/ extra inputs to probe (repeatable)"`
	Jobs     int      `json:"jobs,omitempty" desc:"并发探测数（0=全部并发）/ concurrent probes"`
	Global   bool     `json:"global,omitempty" desc:"探测全部地域（含海外）/ probe all regions"`
	Cn       bool     `json:"cn,omitempty" desc:"只探测中国大陆+港台地域（默认行为）/ probe mainland-China + HK/TW regions only"`
	Listable bool     `json:"listable,omitempty" desc:"只找可匿名列目录的桶 / only anonymously listable buckets"`
	Export   string   `json:"export,omitempty" desc:"导出结果到文件（仅 CLI）/ export results to a file"`
	Color    string   `json:"color,omitempty" desc:"彩色输出 auto|always|never（仅 CLI）" default:"auto"`
	JSON     bool     `json:"-"`
	CLI      bool     `json:"-"`
}

func registerApixFind(reg *registry.Registry) error {
	_, err := spec.Define("find", apixFind).
		Summary(T("探测桶存在于哪些云存储", "find which storage hosts a bucket")).
		Description(T("并发匿名（或带凭证的 SigV4 签名）探测各云厂商，判断桶归属、识别能否匿名列目录。默认只探测中国大陆+港台地域，global=true 探测全部地域；返回每条探测的状态与可匿名列桶的完整 URL 列表。",
			"Probes all known providers concurrently (anonymously, or SigV4-signed when credentials are given) to find which storage hosts each bucket and whether anonymous listing is allowed. By default only mainland-China + HK/TW regions are probed; global=true covers all regions.")).
		CLI(spec.CliHints{
			Usage:   "find <bucket|URL>",
			Aliases: []string{"which"},
			Fields:  apixConnShortcuts(map[string]spec.CliFieldHint{"listable": {Shorthand: "l"}}),
			After: T(`批量输入：位置参数给第一个，更多输入用 --inputs 重复或经 stdin 每行一个（可混用）。
每个输入可以是：
   - 桶名（如 mybucket）→ 并发探测所有已知云厂商的常用区域
   - 完整桶 URL/路径（如 https://mybucket.s3.us-east-1.amazonaws.com/prefix
     或 s3://mybucket/key）→ 只探测该端点（更精确）

探测方式：向桶发 ListObjects 请求，一次请求同时判断存在性 + 能否列目录。
默认匿名探测；配置了凭证（--ak/--sk，STS 加 --token；或 OSS_*/AWS_* 环境变量、
--profile）时自动改为 SigV4 签名探测，可验证非匿名桶。

示例:
   oss find mybucket                           查找单个桶名（命中即流式打印）
   oss find mybucket --inputs bucket-b --inputs bucket-c   多输入（--input 可重复；或 stdin）
   cat buckets.txt | oss find                  批量（stdin 一行一个）
   oss find https://mybucket.s3.us-east-1.amazonaws.com/   完整 URL
   oss find mybucket --listable                只列出可匿名列目录的桶
   oss find mybucket -j                        NDJSON 输出
   oss find mybucket bucket-b --export r.csv   导出 CSV（含 listable_url）
   oss find mybucket --provider aliyun --ak LTAI... --sk ...   用凭证验证私有桶

说明:
   - 默认只探测中国大陆+港台地域（--cn 显式指定同效）；--global 探测全部地域（含海外）；
     --region 只探测指定区域。未找到不代表绝对不存在
   - 两种模式：默认「发现桶存储」——存在即命中（含私有）；--listable 只输出可匿名
     列目录的命中。两种模式都只打印命中的结果、发现即流式输出一行，未命中不显示
   - 腾讯云桶名需含 APPID 后缀；不支持七牛/B2/R2（探测范围外）`,
				`Batch input: the first positional, further inputs via repeatable --input
or stdin (one per line); all can be combined. Each input can be:
   - a bucket name (e.g. mybucket) -> probe all known providers' regions
   - a full bucket URL/path (e.g. https://mybucket.s3.us-east-1.amazonaws.com/prefix
     or s3://mybucket/key) -> probe only that endpoint (more precise)

Probing: one ListObjects request per bucket reveals both existence and
listability. Probes are anonymous by default; with credentials configured
they switch to SigV4-signed probing, which can verify non-anonymous buckets.

EXAMPLES:
   oss find mybucket                            find a single bucket name (hits stream as found)
   oss find mybucket --inputs bucket-b --inputs bucket-c    multi-input (repeatable --input; or stdin)
   cat buckets.txt | oss find                   batch (stdin, one per line)
   oss find https://mybucket.s3.us-east-1.amazonaws.com/   full URL
   oss find mybucket --listable                 only anonymously listable buckets
   oss find mybucket -j                         NDJSON output
   oss find mybucket bucket-b --export r.csv    export CSV (with listable_url)
   oss find mybucket --provider aliyun --ak LTAI... --sk ...   verify a private bucket

NOTES:
   - By default only mainland-China + HK/TW regions are probed (--cn is the
     same); --global probes all regions (incl. overseas); --region probes a
     single region. "not found" is not a guarantee
   - Two modes: the default "find bucket storage" mode treats any existing
     bucket as a hit (private included); --listable prints only anonymously
     listable hits. Both stream one line per hit and hide non-hits
   - Tencent COS bucket names need the APPID suffix; Qiniu/B2/R2 are not probed`),
		}).
		HTTP(xyzHintsGET("/find")).
		MCP(xyzMCPRead()).
		Register(reg)
	return err
}

func apixFind(ctx context.Context, in *apixFindArgs) (*FindReport, error) {
	o := connOptsFrom(in.AK, in.SK, in.Token, in.Profile, in.Provider, in.Endpoint, in.Region,
		in.Proxy, in.Bucket, in.Anonymous, in.PathStyle, in.Insecure, in.Headers, in.Timeout)
	opt := FindOptions{Jobs: in.Jobs, Region: in.Region, Global: in.Global, Cn: in.Cn, Listable: in.Listable}
	raw := in.Inputs
	if in.Input != "" {
		raw = append([]string{in.Input}, raw...)
	}

	if in.CLI {
		return nil, findCLI(ctx, raw, o, opt, in.JSON, in.Export)
	}
	report, err := findTargets(ctx, o, raw, opt, nil, nil, nil)
	if err != nil {
		return nil, errs.WrapMsg(errs.KindInvalidInput, err, err.Error())
	}
	return report, nil
}
