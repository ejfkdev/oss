package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/ejfkdev/xyz-go/registry"
	"github.com/ejfkdev/xyz-go/spec"

	"github.com/ejfkdev/oss/internal/s3x"
)

// StatResult is the structured metadata of a bucket or object, shared by the
// CLI and the HTTP/MCP interfaces.
type StatResult struct {
	Kind         string            `json:"kind"` // bucket | object
	Bucket       string            `json:"bucket"`
	Key          string            `json:"key,omitempty"`
	Provider     string            `json:"provider"`
	Endpoint     string            `json:"endpoint"`
	Region       string            `json:"region"`
	Anonymous    bool              `json:"anonymous"`
	Size         *int64            `json:"size,omitempty"`
	Modified     *time.Time        `json:"modified,omitempty"`
	ETag         string            `json:"etag,omitempty"`
	ContentType  string            `json:"content_type,omitempty"`
	StorageClass string            `json:"storage_class,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

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
	PathStyle bool          `json:"path-style,omitempty" desc:"路径风格寻址 / path-style addressing"`
	Bucket    string        `json:"bucket,omitempty" desc:"桶名（配合 provider 使用）/ bucket name"`
	Proxy     string        `json:"proxy,omitempty" desc:"HTTP(S) 代理 / proxy"`
	Headers   []string      `json:"headers,omitempty" desc:"附加请求头，Key: Value / extra headers"`
	Insecure  bool          `json:"insecure,omitempty" desc:"跳过 TLS 校验 / skip TLS verification"`
	Timeout   time.Duration `json:"timeout,omitempty" desc:"请求超时，如 15s / request timeout"`

	Target string `json:"target,omitempty" desc:"目标桶或对象（s3://… 或 URL）/ target bucket or object" cli:"positional"`
	Color  string `json:"color,omitempty" desc:"彩色输出 auto|always|never（仅 CLI）" default:"auto"`
	JSON   bool   `json:"-"`
	CLI    bool   `json:"-"`
}

func registerApixStat(reg *registry.Registry) error {
	_, err := spec.Define("stat", apixStat).
		Summary(T("查看桶或对象的元数据", "show bucket or object metadata")).
		Description(T("返回目标桶的连接信息，或对象的大小、修改时间、ETag、Content-Type、存储类型与自定义元数据。",
			"Returns connection info for a bucket target, or size / modified / etag / content-type / storage-class / custom metadata for an object.")).
		CLI(spec.CliHints{
			Usage:  "stat <target>",
			Fields: apixConnShortcuts(nil),
			After: T(`示例:
   oss stat s3://mybucket/file.tar.gz             查看对象元数据（大小/etag/类型/自定义 metadata）
   oss stat s3://mybucket                         验证桶可达性，显示连接信息
   oss stat https://bucket.s3.us-east-1.amazonaws.com/key  URL 方式查看`,
				`EXAMPLES:
   oss stat s3://mybucket/file.tar.gz             show object metadata (size/etag/type/custom metadata)
   oss stat s3://mybucket                         check bucket reachability and connection info
   oss stat https://bucket.s3.us-east-1.amazonaws.com/key  query via URL`),
		}).
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
		if in.CLI {
			return nil, err
		}
		return nil, apixErr(o.Anonymous, err)
	}
	if in.CLI {
		renderStat(res)
		return nil, nil
	}
	return res, nil
}

// renderStat prints the metadata in the pre-migration labeled layout
// (colored, display-width-aligned, CJK-aware).
func renderStat(res *StatResult) {
	lab := statLabel()
	if res.Kind == "bucket" {
		fmt.Printf("%s %s\n", lab(T("bucket:", "bucket:")), cGreen(res.Bucket))
		fmt.Printf("%s %s\n", lab(T("厂商:", "provider:")), res.Provider)
		fmt.Printf("%s %s\n", lab(T("端点:", "endpoint:")), res.Endpoint)
		fmt.Printf("%s %s\n", lab(T("区域:", "region:")), res.Region)
		fmt.Printf("%s %v\n", lab(T("匿名:", "anonymous:")), res.Anonymous)
		return
	}
	size := int64(0)
	if res.Size != nil {
		size = *res.Size
	}
	fmt.Printf("%s %s\n", lab(T("键:", "key:")), cBold(res.Key))
	fmt.Printf("%s %s %s\n", lab(T("大小:", "size:")),
		sizeColored(humanSize(size, false), size), cDim(fmt.Sprintf("(%d bytes)", size)))
	if res.Modified != nil {
		fmt.Printf("%s %s\n", lab(T("修改时间:", "modified:")), humanTime(res.Modified))
	}
	fmt.Printf("%s %s\n", lab("etag:"), res.ETag)
	if res.ContentType != "" {
		fmt.Printf("%s %s\n", lab(T("类型:", "content-type:")), res.ContentType)
	}
	if res.StorageClass != "" {
		fmt.Printf("%s %s\n", lab(T("存储类型:", "storage-class:")), res.StorageClass)
	}
	if len(res.Metadata) > 0 {
		keys := make([]string, 0, len(res.Metadata))
		for k := range res.Metadata {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Printf("%s\n", lab(T("元数据:", "metadata:")))
		for _, k := range keys {
			fmt.Printf("  %s %s\n", cCyan(k+":"), res.Metadata[k])
		}
	}
}

// statTarget resolves target and fetches bucket or object metadata.
func statTarget(ctx context.Context, o *s3x.ConnOpts, target string) (*StatResult, error) {
	t, err := s3x.ParseTarget(target, o)
	if err != nil {
		return nil, err
	}
	if t == nil || t.Bucket == "" {
		return nil, errors.New(T("用法: oss stat <桶[/对象]>", "usage: oss stat <bucket[/object]>"))
	}
	cl, err := s3x.New(ctx, o, t)
	if err != nil {
		return nil, err
	}

	conn := StatResult{
		Kind: "bucket", Bucket: t.Bucket,
		Provider: cl.Provider, Endpoint: cl.Endpoint, Region: cl.Region, Anonymous: cl.Anonymous,
	}
	if t.Key == "" {
		if _, err := cl.S3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(t.Bucket)}); err != nil {
			return nil, apiErr(err, cl.Anonymous)
		}
		return &conn, nil
	}

	out, err := cl.S3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(t.Bucket), Key: aws.String(t.Key),
	})
	if err != nil {
		return nil, apiErr(err, cl.Anonymous)
	}
	conn.Kind = "object"
	conn.Key = t.Key
	conn.Size = out.ContentLength
	conn.Modified = out.LastModified
	conn.ETag = aws.ToString(out.ETag)
	conn.ContentType = aws.ToString(out.ContentType)
	conn.StorageClass = string(out.StorageClass)
	conn.Metadata = out.Metadata
	return &conn, nil
}

// statLabel returns a colored, display-width-aligned label renderer
// (Chinese labels contain double-width characters, so plain %-Ns padding
// would misalign them).
func statLabel() func(string) string {
	return func(s string) string {
		return cCyan(padDisplay(s, 14))
	}
}
