package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	errs "github.com/ejfkdev/xyz-go/errors"
	"github.com/ejfkdev/xyz-go/registry"
	"github.com/ejfkdev/xyz-go/spec"

	"github.com/ejfkdev/oss/internal/s3x"
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
	PathStyle bool          `json:"path-style,omitempty" desc:"路径风格寻址 / path-style addressing"`
	Bucket    string        `json:"bucket,omitempty" desc:"桶名（配合 provider 使用）/ bucket name"`
	Proxy     string        `json:"proxy,omitempty" desc:"HTTP(S) 代理 / proxy"`
	Headers   []string      `json:"headers,omitempty" desc:"附加请求头，Key: Value / extra headers"`
	Insecure  bool          `json:"insecure,omitempty" desc:"跳过 TLS 校验 / skip TLS verification"`
	Timeout   time.Duration `json:"timeout,omitempty" desc:"请求超时，如 15s / request timeout"`

	Target  string        `json:"target,omitempty" desc:"目标对象（s3://… 或 URL）/ target object" cli:"positional"`
	Expires time.Duration `json:"expires,omitempty" desc:"链接有效期，如 15m / URL validity" default:"15m"`
	Method  string        `json:"method,omitempty" desc:"签名的 HTTP 方法 / method to sign" default:"GET" enum:"GET,PUT"`
	Color   string        `json:"color,omitempty" desc:"彩色输出 auto|always|never（仅 CLI）" default:"auto"`
	JSON    bool          `json:"-"`
	CLI     bool          `json:"-"`
}

// apixPresignResp carries the pre-signed URL back.
type apixPresignResp struct {
	URL     string `json:"url"`
	Method  string `json:"method"`
	Expires string `json:"expires"`
}

func registerApixPresign(reg *registry.Registry) error {
	_, err := spec.Define("presign", apixPresign).
		Summary(T("生成对象的预签名 URL", "generate a pre-signed URL for an object")).
		Description(T("为对象生成限时预签名链接（默认 15 分钟，GET 或 PUT）。需要凭证——匿名访问返回 unauthorized。",
			"Generates an expiring pre-signed URL (15 minutes by default, GET or PUT). Requires credentials; anonymous access is rejected as unauthorized.")).
		CLI(spec.CliHints{
			Usage:  "presign <target-object>",
			Fields: apixConnShortcuts(nil),
			After: T(`示例:
   oss presign s3://mybucket/file.tar.gz                     默认 15 分钟下载链接
   oss presign s3://mybucket/file.tar.gz --expires 24h       有效期 24 小时
   oss presign s3://mybucket/upload.bin --method PUT         上传链接
   curl -o f.tar.gz "$(oss presign s3://mybucket/f.tar.gz)"  配合其它工具分享

说明: 预签名需要凭证（--ak/--sk、环境变量或 --profile），匿名不可用。`,
				`EXAMPLES:
   oss presign s3://mybucket/file.tar.gz                     download link (15 minutes)
   oss presign s3://mybucket/file.tar.gz --expires 24h       24h validity
   oss presign s3://mybucket/upload.bin --method PUT         upload link
   curl -o f.tar.gz "$(oss presign s3://mybucket/f.tar.gz)"  share with other tools

NOTE: presigning requires credentials (--ak/--sk, env or --profile).`),
		}).
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
		if in.CLI {
			return nil, err
		}
		return nil, apixErr(o.Anonymous, err)
	}
	if in.CLI {
		fmt.Println(url)
		return nil, nil
	}
	return &apixPresignResp{URL: url, Method: in.Method, Expires: in.Expires.String()}, nil
}

// presignTarget produces a pre-signed URL for the object at target.
func presignTarget(ctx context.Context, o *s3x.ConnOpts, target, method string, expires time.Duration) (string, error) {
	t, err := s3x.ParseTarget(target, o)
	if err != nil {
		return "", err
	}
	if t == nil || t.Bucket == "" || t.Key == "" {
		return "", errors.New(T("用法: oss presign <桶/对象>", "usage: oss presign <bucket/object>"))
	}
	cl, err := s3x.New(ctx, o, t)
	if err != nil {
		return "", err
	}
	if cl.Anonymous {
		return "", errs.New(errs.KindUnauthorized, T("预签名需要凭证（--ak/--sk、环境变量或 --profile）",
			"presigning requires credentials (--ak/--sk, env or --profile)"))
	}

	pc := s3.NewPresignClient(cl.S3)
	switch strings.ToUpper(method) {
	case "GET", "":
		res, err := pc.PresignGetObject(ctx,
			&s3.GetObjectInput{Bucket: aws.String(t.Bucket), Key: aws.String(t.Key)},
			s3.WithPresignExpires(expires))
		if err != nil {
			return "", err
		}
		return res.URL, nil
	case "PUT":
		res, err := pc.PresignPutObject(ctx,
			&s3.PutObjectInput{Bucket: aws.String(t.Bucket), Key: aws.String(t.Key)},
			s3.WithPresignExpires(expires))
		if err != nil {
			return "", err
		}
		return res.URL, nil
	default:
		return "", fmt.Errorf("unsupported --method %q (want GET or PUT)", method)
	}
}
