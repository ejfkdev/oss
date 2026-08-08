package app

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/urfave/cli/v3"

	"github.com/ejfkdev/oss/internal/s3x"
)

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
		Action:    runStat,
	}
}

func runStat(ctx context.Context, c *cli.Command) error {
	o := connOpts(c)
	t, err := s3x.ParseTarget(c.Args().First(), o)
	if err != nil {
		return err
	}
	if t == nil || t.Bucket == "" {
		return errors.New(T("用法: oss stat <桶[/对象]>", "usage: oss stat <bucket[/object]>"))
	}
	cl, err := s3x.New(ctx, o, t)
	if err != nil {
		return err
	}

	if t.Key == "" {
		_, err := cl.S3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(t.Bucket)})
		if err != nil {
			return apiErr(err, cl.Anonymous)
		}
		lab := statLabel()
		fmt.Printf("%s %s\n", lab(T("bucket:", "bucket:")), cGreen(t.Bucket))
		fmt.Printf("%s %s\n", lab(T("厂商:", "provider:")), cl.Provider)
		fmt.Printf("%s %s\n", lab(T("端点:", "endpoint:")), cl.Endpoint)
		fmt.Printf("%s %s\n", lab(T("区域:", "region:")), cl.Region)
		fmt.Printf("%s %v\n", lab(T("匿名:", "anonymous:")), cl.Anonymous)
		return nil
	}

	out, err := cl.S3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(t.Bucket), Key: aws.String(t.Key),
	})
	if err != nil {
		return apiErr(err, cl.Anonymous)
	}
	lab := statLabel()
	size := aws.ToInt64(out.ContentLength)
	fmt.Printf("%s %s\n", lab(T("键:", "key:")), cBold(t.Key))
	fmt.Printf("%s %s %s\n", lab(T("大小:", "size:")),
		sizeColored(humanSize(size, false), size), cDim(fmt.Sprintf("(%d bytes)", size)))
	fmt.Printf("%s %s\n", lab(T("修改时间:", "modified:")), humanTime(out.LastModified))
	fmt.Printf("%s %s\n", lab("etag:"), aws.ToString(out.ETag))
	fmt.Printf("%s %s\n", lab(T("类型:", "content-type:")), aws.ToString(out.ContentType))
	if out.StorageClass != "" {
		fmt.Printf("%s %s\n", lab(T("存储类型:", "storage-class:")), out.StorageClass)
	}
	if len(out.Metadata) > 0 {
		keys := make([]string, 0, len(out.Metadata))
		for k := range out.Metadata {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Printf("%s\n", lab(T("元数据:", "metadata:")))
		for _, k := range keys {
			fmt.Printf("  %s %s\n", cCyan(k+":"), out.Metadata[k])
		}
	}
	return nil
}

// statLabel returns a colored, display-width-aligned label renderer
// (Chinese labels contain double-width characters, so plain %-Ns padding
// would misalign them).
func statLabel() func(string) string {
	return func(s string) string {
		return cCyan(padDisplay(s, 14))
	}
}
