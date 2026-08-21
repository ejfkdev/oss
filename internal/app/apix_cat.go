package app

import (
	"context"
	"encoding/base64"
	"io"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	errs "github.com/ejfkdev/xyz-go/errors"
	"github.com/ejfkdev/xyz-go/registry"
	"github.com/ejfkdev/xyz-go/spec"
)

// mcpCatMaxBytes caps the MCP cat response. MCP results must fit in memory and
// travel through JSON, unlike the CLI cat and the raw HTTP /cat route, which
// stream; larger objects should use one of those.
const mcpCatMaxBytes = 16 << 20

// apixCatArgs follows the canonical connection fields documented in apix.go.
// It is an MCP-only tool (no HTTP hint): the HTTP surface uses the raw
// streaming GET /cat route instead.
type apixCatArgs struct {
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

	Target string `json:"target" desc:"目标对象（s3://… 或 URL）/ target object" required:"true"`
	Range  string `json:"range,omitempty" desc:"字节范围，如 0-99 或 bytes=0-99 / byte range"`
}

// apixCatResp carries the object content back: Text for valid UTF-8 content,
// Base64 otherwise (never both). Size and ContentType always accompany it.
type apixCatResp struct {
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
	Text        string `json:"text,omitempty"`
	Base64      string `json:"base64,omitempty"`
}

func registerApixCat(reg *registry.Registry) error {
	_, err := spec.Define("cat", apixCat).
		Summary("读取对象内容 / read object content").
		Description("读取对象内容（可选 range 字节范围）。UTF-8 文本放 text 字段，二进制内容放 base64 字段（二选一）。单次上限 16MiB——更大的对象请用 CLI `oss cat` 或 HTTP `GET /cat`（原始字节流）。\n\nReads object content (optional byte range). UTF-8 text goes into the text field, binary content into base64 (one of the two). Capped at 16MiB per call — for larger objects use the CLI `oss cat` or HTTP `GET /cat` (raw byte stream).").
		MCP(xyzMCPRead()).
		Register(reg)
	return err
}

func apixCat(ctx context.Context, in *apixCatArgs) (*apixCatResp, error) {
	o := connOptsFrom(in.AK, in.SK, in.Token, in.Profile, in.Provider, in.Endpoint, in.Region,
		in.Proxy, in.Bucket, in.Anonymous, in.PathStyle, in.Insecure, in.Headers, in.Timeout)
	_, resp, err := catTarget(ctx, o, in.Target, in.Range)
	if err != nil {
		return nil, apixErr(o.Anonymous, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, mcpCatMaxBytes+1))
	if err != nil {
		return nil, apixErr(o.Anonymous, err)
	}
	if len(data) > mcpCatMaxBytes {
		return nil, errs.New(errs.KindInvalidInput, T(
			"对象超过 16MiB——MCP 传输不适合大文件，请用 CLI: oss cat 或 HTTP: GET /cat",
			"object exceeds 16MiB — MCP is not for large files; use the CLI `oss cat` or HTTP `GET /cat`"))
	}
	out := &apixCatResp{
		ContentType: aws.ToString(resp.ContentType),
		Size:        int64(len(data)),
	}
	if utf8.Valid(data) {
		out.Text = string(data)
	} else {
		out.Base64 = base64.StdEncoding.EncodeToString(data)
	}
	return out, nil
}
