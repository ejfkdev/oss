package app

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/urfave/cli/v3"

	"github.com/ejfkdev/oss/internal/s3x"
)

func catCmd() *cli.Command {
	flags := append([]cli.Flag{
		&cli.StringFlag{Name: "range", Usage: T("字节范围，如 0-99 或 bytes=0-99", "byte range, e.g. 0-99 or bytes=0-99")},
	}, connFlags()...)
	return &cli.Command{
		Name:  "cat",
		Usage: T("输出对象内容到 stdout", "print object content to stdout"),
		UsageText: T(`oss cat <目标对象> [--range <范围>]

示例:
   oss cat s3://mybucket/config.yaml               输出对象内容
   oss cat s3://mybucket/app.log --range 0-1023    只读前 1 KB
   oss cat s3://mybucket/data.bin --range 100-     从偏移 100 读到末尾
   oss cat s3://mybucket/config.yaml | head -5     配合管道使用
   oss cat https://bucket.s3.us-east-1.amazonaws.com/config.yaml
                                                   URL 匿名访问公共桶

--range 格式: "0-1023"、"bytes=0-1023"、"100-"（开区间）。`,
			`oss cat <target-object> [--range <range>]

EXAMPLES:
   oss cat s3://mybucket/config.yaml               print object content
   oss cat s3://mybucket/app.log --range 0-1023    read only the first 1 KB
   oss cat s3://mybucket/data.bin --range 100-     read from offset 100 to the end
   oss cat s3://mybucket/config.yaml | head -5     works with pipes
   oss cat https://bucket.s3.us-east-1.amazonaws.com/config.yaml
                                                   anonymous URL access

--range formats: "0-1023", "bytes=0-1023", "100-" (open end).`),
		Flags:  flags,
		Action: runCat,
	}
}

func runCat(ctx context.Context, c *cli.Command) error {
	_, resp, err := catTarget(ctx, connOpts(c), c.Args().First(), c.String("range"))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
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

	in := &s3.GetObjectInput{Bucket: aws.String(t.Bucket), Key: aws.String(t.Key)}
	if r := strings.TrimSpace(rng); r != "" {
		if !strings.HasPrefix(r, "bytes=") {
			r = "bytes=" + r
		}
		in.Range = aws.String(r)
	}

	resp, err := cl.S3.GetObject(ctx, in)
	if err != nil {
		return nil, nil, apiErr(err, cl.Anonymous)
	}
	return cl, resp, nil
}
