package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/urfave/cli/v3"

	"github.com/ejfkdev/oss/internal/s3x"
)

func presignCmd() *cli.Command {
	flags := append([]cli.Flag{
		&cli.DurationFlag{Name: "expires", Value: 15 * time.Minute, Usage: T("链接有效期，如 15m、1h", "URL validity, e.g. 15m, 1h")},
		&cli.StringFlag{Name: "method", Value: "GET", Usage: T("签名的 HTTP 方法: GET|PUT", "HTTP method to sign: GET|PUT")},
	}, connFlags()...)
	return &cli.Command{
		Name:  "presign",
		Usage: T("生成对象的预签名 URL", "generate a pre-signed URL for an object"),
		UsageText: T(`oss presign <目标对象> [--expires 15m] [--method GET|PUT]

示例:
   oss presign s3://mybucket/file.tar.gz                     生成下载链接（默认有效 15 分钟）
   oss presign s3://mybucket/file.tar.gz --expires 24h       有效期 24 小时
   oss presign s3://mybucket/upload.bin --method PUT         生成上传链接
   curl -o f.tar.gz "$(oss presign s3://mybucket/f.tar.gz)"  配合其它工具分享

说明: 预签名需要凭证（--ak/--sk、环境变量或 --profile），匿名不可用；
链接在有效期内可被任何持有者直接访问。`,
			`oss presign <target-object> [--expires 15m] [--method GET|PUT]

EXAMPLES:
   oss presign s3://mybucket/file.tar.gz                     download link (default validity 15m)
   oss presign s3://mybucket/file.tar.gz --expires 24h       valid for 24 hours
   oss presign s3://mybucket/upload.bin --method PUT         upload link
   curl -o f.tar.gz "$(oss presign s3://mybucket/f.tar.gz)"  share with other tools

NOTE: presigning requires credentials (--ak/--sk, env or --profile);
anonymous is not supported. Anyone holding the URL can access it until expiry.`),
		Flags:  flags,
		Action:    runPresign,
	}
}

func runPresign(ctx context.Context, c *cli.Command) error {
	o := connOpts(c)
	t, err := s3x.ParseTarget(c.Args().First(), o)
	if err != nil {
		return err
	}
	if t == nil || t.Bucket == "" || t.Key == "" {
		return errors.New(T("用法: oss presign <桶/对象>", "usage: oss presign <bucket/object>"))
	}
	cl, err := s3x.New(ctx, o, t)
	if err != nil {
		return err
	}
	if cl.Anonymous {
		return errors.New(T("预签名需要凭证（--ak/--sk、环境变量或 --profile）",
			"presigning requires credentials (--ak/--sk, env or --profile)"))
	}

	expires := c.Duration("expires")
	pc := s3.NewPresignClient(cl.S3)

	switch strings.ToUpper(c.String("method")) {
	case "GET", "":
		res, err := pc.PresignGetObject(ctx,
			&s3.GetObjectInput{Bucket: aws.String(t.Bucket), Key: aws.String(t.Key)},
			s3.WithPresignExpires(expires))
		if err != nil {
			return err
		}
		fmt.Println(res.URL)
	case "PUT":
		res, err := pc.PresignPutObject(ctx,
			&s3.PutObjectInput{Bucket: aws.String(t.Bucket), Key: aws.String(t.Key)},
			s3.WithPresignExpires(expires))
		if err != nil {
			return err
		}
		fmt.Println(res.URL)
	default:
		return fmt.Errorf("unsupported --method %q (want GET or PUT)", c.String("method"))
	}
	return nil
}
