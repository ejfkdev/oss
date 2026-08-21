package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/urfave/cli/v3"

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

func statCmd() *cli.Command {
	flags := connFlags()
	return &cli.Command{
		Name:  "stat",
		Usage: T("查看桶或对象的元数据", "show bucket or object metadata"),
		UsageText: T(`oss stat <目标>

示例:
   oss stat s3://mybucket/file.tar.gz             查看对象元数据（大小/etag/类型/自定义 metadata）
   oss stat s3://mybucket                         验证桶可达性，显示连接信息
   oss stat https://files.example.com/bucket/key  URL 方式查看
   oss stat s3://mybucket/file.tar.gz --provider aliyun --ak <AK> --sk <SK>
                                                  指定厂商与凭证`,
			`oss stat <target>

EXAMPLES:
   oss stat s3://mybucket/file.tar.gz             show object metadata (size/etag/type/custom metadata)
   oss stat s3://mybucket                         check bucket reachability and connection info
   oss stat https://files.example.com/bucket/key  query via URL
   oss stat s3://mybucket/file.tar.gz --provider aliyun --ak <AK> --sk <SK>
                                                  with explicit provider and credentials`),
		Flags:  flags,
		Action: runStat,
	}
}

func runStat(ctx context.Context, c *cli.Command) error {
	res, err := statTarget(ctx, connOpts(c), c.Args().First())
	if err != nil {
		return err
	}
	lab := statLabel()
	if res.Kind == "bucket" {
		fmt.Printf("%s %s\n", lab(T("bucket:", "bucket:")), cGreen(res.Bucket))
		fmt.Printf("%s %s\n", lab(T("厂商:", "provider:")), res.Provider)
		fmt.Printf("%s %s\n", lab(T("端点:", "endpoint:")), res.Endpoint)
		fmt.Printf("%s %s\n", lab(T("区域:", "region:")), res.Region)
		fmt.Printf("%s %v\n", lab(T("匿名:", "anonymous:")), res.Anonymous)
		return nil
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
	return nil
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
