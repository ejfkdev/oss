package app

import (
	"context"
	"time"

	"github.com/ejfkdev/xyz-go/registry"
	"github.com/ejfkdev/xyz-go/spec"
)

// apixStatArgs follows the canonical connection fields documented in apix.go.
type apixStatArgs struct {
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

	Target string `json:"target" desc:"目标桶或对象（s3://… 或 URL）/ target bucket or object" required:"true"`
}

func registerApixStat(reg *registry.Registry) error {
	_, err := spec.Define("stat", apixStat).
		Summary("查看桶或对象的元数据 / bucket or object metadata").
		Description("返回目标桶的连接信息，或对象的大小、修改时间、ETag、Content-Type、存储类型与自定义元数据。\n\nReturns connection info for a bucket target, or size / modified / etag / content-type / storage-class / custom metadata for an object.").
		HTTP(xyzHintsGET("/stat")).
		MCP(xyzMCPRead()).
		Register(reg)
	return err
}

func apixStat(ctx context.Context, in *apixStatArgs) (*StatResult, error) {
	o := connOptsFrom(in.AK, in.SK, in.Token, in.Profile, in.Provider, in.Endpoint, in.Region,
		in.Proxy, in.Bucket, in.Anonymous, in.PathStyle, in.Insecure, in.Headers, in.Timeout)
	res, err := statTarget(ctx, o, in.Target)
	if err != nil {
		return nil, apixErr(o.Anonymous, err)
	}
	return res, nil
}
