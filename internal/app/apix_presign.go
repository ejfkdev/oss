package app

import (
	"context"
	"time"

	"github.com/ejfkdev/xyz-go/registry"
	"github.com/ejfkdev/xyz-go/spec"
)

// apixPresignArgs follows the canonical connection fields documented in
// apix.go.
type apixPresignArgs struct {
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

	Target  string        `json:"target" desc:"目标对象（s3://… 或 URL）/ target object" required:"true"`
	Expires time.Duration `json:"expires,omitempty" desc:"链接有效期，如 15m / URL validity" default:"15m"`
	Method  string        `json:"method,omitempty" desc:"签名的 HTTP 方法 / method to sign" default:"GET" enum:"GET,PUT"`
}

// apixPresignResp carries the pre-signed URL back.
type apixPresignResp struct {
	URL     string `json:"url"`
	Method  string `json:"method"`
	Expires string `json:"expires"`
}

func registerApixPresign(reg *registry.Registry) error {
	_, err := spec.Define("presign", apixPresign).
		Summary("生成对象的预签名 URL / pre-sign an object URL").
		Description("为对象生成限时预签名链接（默认 15 分钟，GET 或 PUT）。需要凭证——匿名访问返回 unauthorized。\n\nGenerates an expiring pre-signed URL (15 minutes by default, GET or PUT). Requires credentials; anonymous access is rejected as unauthorized.").
		HTTP(xyzHintsGET("/presign")).
		MCP(xyzMCPRead()).
		Register(reg)
	return err
}

func apixPresign(ctx context.Context, in *apixPresignArgs) (*apixPresignResp, error) {
	o := connOptsFrom(in.AK, in.SK, in.Token, in.Profile, in.Provider, in.Endpoint, in.Region,
		in.Proxy, in.Bucket, in.Anonymous, in.PathStyle, in.Insecure, in.Headers, in.Timeout)
	url, err := presignTarget(ctx, o, in.Target, in.Method, in.Expires)
	if err != nil {
		return nil, apixErr(o.Anonymous, err)
	}
	return &apixPresignResp{URL: url, Method: in.Method, Expires: in.Expires.String()}, nil
}
