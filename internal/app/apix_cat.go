package app

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	errs "github.com/ejfkdev/xyz-go/errors"
	"github.com/ejfkdev/xyz-go/registry"
	"github.com/ejfkdev/xyz-go/spec"

	"github.com/ejfkdev/oss/internal/s3x"
)

// mcpCatMaxBytes caps the MCP cat response. MCP results must fit in memory and
// travel through JSON, unlike the CLI cat and the raw HTTP /cat route, which
// stream; larger objects should use one of those.
const mcpCatMaxBytes = 16 << 20

// apixCatArgs follows the canonical connection fields documented in apix.go.
// It has no HTTP hint: the HTTP surface uses the raw streaming GET /cat route
// under serve; the CLI and MCP share this definition.
type apixCatArgs struct {
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

	Target string `json:"target,omitempty" desc:"目标对象（s3://… 或 URL）/ target object" cli:"positional"`
	Range  string `json:"range,omitempty" desc:"字节范围，如 0-99 或 bytes=0-99 / byte range"`
	Color  string `json:"color,omitempty" desc:"彩色输出 auto|always|never（仅 CLI）" default:"auto"`
	JSON   bool   `json:"-"`
	CLI    bool   `json:"-"`
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
		Summary(T("输出对象内容", "print object content")).
		Description(T("读取对象内容（可选 range 字节范围）。CLI 输出为原始字节流；MCP 侧 UTF-8 文本放 text 字段、二进制放 base64，单次上限 16MiB。",
			"Reads object content (optional byte range). The CLI streams raw bytes; over MCP UTF-8 text goes into the text field and binary into base64, capped at 16MiB.")).
		CLI(spec.CliHints{
			Usage:  "cat <target-object>",
			Fields: apixConnShortcuts(nil),
			After: T(`示例:
   oss cat s3://mybucket/config.yaml               输出对象内容
   oss cat s3://mybucket/app.log --range 0-1023    只读前 1 KB
   oss cat s3://mybucket/data.bin --range 100-     从偏移 100 读到末尾
   oss cat s3://mybucket/config.yaml | head -5     配合管道使用
   oss cat https://bucket.s3.us-east-1.amazonaws.com/config.yaml   URL 匿名访问

--range 格式: "0-1023"、"bytes=0-1023"、"100-"（开区间）。`,
				`EXAMPLES:
   oss cat s3://mybucket/config.yaml               print object content
   oss cat s3://mybucket/app.log --range 0-1023    read only the first 1 KB
   oss cat s3://mybucket/data.bin --range 100-     read from offset 100 to the end
   oss cat s3://mybucket/config.yaml | head -5     works with pipes
   oss cat https://bucket.s3.us-east-1.amazonaws.com/config.yaml   anonymous URL access

--range formats: "0-1023", "bytes=0-1023", "100-" (open end).`),
		}).
		MCP(xyzMCPRead()).
		Register(reg)
	return err
}

func apixCat(ctx context.Context, in *apixCatArgs) (*apixCatResp, error) {
	o := connOptsFrom(in.AK, in.SK, in.Token, in.Profile, in.Provider, in.Endpoint, in.Region,
		in.Proxy, in.Bucket, in.Anonymous, in.PathStyle, in.Insecure, in.Headers, in.Timeout)
	if in.CLI {
		_, resp, err := catTarget(ctx, o, in.Target, in.Range)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		_, err = io.Copy(os.Stdout, resp.Body)
		return nil, err
	}

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

// catTarget fetches the object at target (optionally limited to rng, where a
// missing "bytes=" prefix is added) and returns the client plus the open
// response body. The caller owns the body and closes it; this is the shared
// core of the CLI cat, the HTTP GET /cat raw stream and the MCP cat tool.
func catTarget(ctx context.Context, o *s3x.ConnOpts, target, rng string) (*s3x.Client, *s3.GetObjectOutput, error) {
	t, err := s3x.ParseTarget(target, o)
	if err != nil {
		return nil, nil, err
	}
	if t == nil || t.Bucket == "" || t.Key == "" {
		return nil, nil, errors.New(T("用法: oss cat <桶/对象>", "usage: oss cat <bucket/object>"))
	}
	cl, err := s3x.New(ctx, o, t)
	if err != nil {
		return nil, nil, err
	}

	inp := &s3.GetObjectInput{Bucket: aws.String(t.Bucket), Key: aws.String(t.Key)}
	if r := strings.TrimSpace(rng); r != "" {
		if !strings.HasPrefix(r, "bytes=") {
			r = "bytes=" + r
		}
		inp.Range = aws.String(r)
	}

	resp, err := cl.S3.GetObject(ctx, inp)
	if err != nil {
		return nil, nil, apiErr(err, cl.Anonymous)
	}
	return cl, resp, nil
}
