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
	PathStyle bool          `json:"path_style,omitempty" desc:"路径风格寻址 / path-style addressing"`
	Bucket    string        `json:"bucket,omitempty" desc:"桶名（配合 provider 使用）/ bucket name"`
	Proxy     string        `json:"proxy,omitempty" desc:"HTTP(S) 代理 / proxy"`
	Headers   []string      `json:"headers,omitempty" desc:"附加请求头，Key: Value / extra headers"`
	Insecure  bool          `json:"insecure,omitempty" desc:"跳过 TLS 校验 / skip TLS verification"`
	Timeout   time.Duration `json:"timeout,omitempty" desc:"请求超时，如 15s / request timeout"`

	Inputs   []string `json:"inputs" desc:"要探测的桶名或完整 URL（可多个，如 find 的 CLI 批处理）/ bucket names or full URLs to probe" required:"true"`
	Jobs     int      `json:"jobs,omitempty" desc:"并发探测数（0=全部并发）/ concurrent probes"`
	Global   bool     `json:"global,omitempty" desc:"探测全部地域（含海外）/ probe all regions"`
	Cn       bool     `json:"cn,omitempty" desc:"只探测中国大陆+港台地域（默认行为）/ probe mainland-China + HK/TW regions only"`
	Listable bool     `json:"listable,omitempty" desc:"只找可匿名列目录的桶 / only anonymously listable buckets"`
}

func registerApixFind(reg *registry.Registry) error {
	_, err := spec.Define("find", apixFind).
		Summary("探测桶存在于哪些云存储 / find which storage hosts a bucket").
		Description("并发匿名（或带凭证的 SigV4 签名）探测各云厂商，判断桶归属、识别能否匿名列目录。默认只探测中国大陆+港台地域，global=true 探测全部地域；返回每条探测的状态与可匿名列桶的完整 URL 列表。\n\nProbes all known providers concurrently (anonymously, or SigV4-signed when credentials are given) to find which storage hosts each bucket and whether anonymous listing is allowed. By default only mainland-China + HK/TW regions are probed; global=true covers all regions.").
		HTTP(xyzHintsGET("/find")).
		MCP(xyzMCPRead()).
		Register(reg)
	return err
}

func apixFind(ctx context.Context, in *apixFindArgs) (*FindReport, error) {
	o := connOptsFrom(in.AK, in.SK, in.Token, in.Profile, in.Provider, in.Endpoint, in.Region,
		in.Proxy, in.Bucket, in.Anonymous, in.PathStyle, in.Insecure, in.Headers, in.Timeout)
	report, err := findTargets(ctx, o, in.Inputs, FindOptions{
		Jobs: in.Jobs, Region: in.Region, Global: in.Global, Cn: in.Cn, Listable: in.Listable,
	}, nil, nil, nil)
	if err != nil {
		return nil, errs.WrapMsg(errs.KindInvalidInput, err, err.Error())
	}
	return report, nil
}
