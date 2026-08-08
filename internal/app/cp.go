package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"

	"github.com/ejfkdev/oss/internal/s3x"
)

func cpCmd() *cli.Command {
	flags := append([]cli.Flag{
		// ---- scope & filters ----
		&cli.BoolFlag{Name: "recursive", Aliases: []string{"r"}, Usage: T("按前缀/目录递归拷贝", "copy recursively by prefix/directory")},
		&cli.StringSliceFlag{Name: "include", Usage: T("glob 包含过滤，可重复（匹配相对路径或文件名）", "glob include filter, repeatable (matches relative path or base name)")},
		&cli.StringSliceFlag{Name: "exclude", Usage: T("glob 排除过滤，可重复（如 *.tmp）", "glob exclude filter, repeatable (e.g. *.tmp)")},
		&cli.BoolFlag{Name: "skip-existing", Usage: T("目标已存在则跳过", "skip files that already exist at the destination")},
		// ---- concurrency ----
		&cli.IntFlag{Name: "jobs", Value: 16, Usage: T("并发文件数", "parallel file transfers")},
		&cli.IntFlag{Name: "parallel", Value: 5, Usage: T("单文件分片并发数（multipart）", "parallel parts per file (multipart)")},
		// ---- output ----
		&cli.BoolFlag{Name: "no-progress", Usage: T("关闭进度显示", "disable progress output")},
	}, connFlags()...)
	return &cli.Command{
		Name:  "cp",
		Usage: T("下载 / 上传 / 跨桶拷贝", "download / upload / cross-bucket copy"),
		UsageText: T(`oss cp <源> <目标>

cp 负责所有文件传输：下载、上传、跨桶拷贝。查看列表请用 ls。
远端必须带 scheme: s3:// oss:// cos:// obs:// http(s)://

示例:
   oss cp s3://bucket/path/file.tar.gz .                    下载单文件
   oss cp s3://bucket/path/file.tar.gz -                    下载到 stdout
   oss cp -r s3://bucket/logs/ ./logs --include "*.gz"      按条件批量下载
   oss cp -r https://bucket.s3.amazonaws.com/dataset/ ./data --jobs 32
   oss cp -r s3://bucket/logs/ ./logs --exclude "*.tmp" --skip-existing
   oss cp ./dist s3://bucket/release/ -r                    上传目录
   oss cp s3://bucket-a/k.bin s3://bucket-b/k.bin           服务端拷贝

目录结构说明:
   -r 递归下载时按 key 中的 / 逐级创建本地目录；源以 / 结尾时，
   其内容直接放入目标目录（如 s3://b/logs/ → ./logs/<日志文件>）。

参数分类:
   范围: -r/--recursive  --include  --exclude  --skip-existing
   性能: --jobs（并发文件数）  --parallel（单文件分片数）
   输出: --no-progress`,
			`oss cp <src> <dst>

cp handles all file transfers: download, upload, cross-bucket copy. Use ls to view lists.
Remote endpoints need a scheme: s3:// oss:// cos:// obs:// http(s)://

EXAMPLES:
   oss cp s3://bucket/path/file.tar.gz .                    download one file
   oss cp s3://bucket/path/file.tar.gz -                    download to stdout
   oss cp -r s3://bucket/logs/ ./logs --include "*.gz"      filtered batch download
   oss cp -r https://bucket.s3.amazonaws.com/dataset/ ./data --jobs 32
   oss cp -r s3://bucket/logs/ ./logs --exclude "*.tmp" --skip-existing
   oss cp ./dist s3://bucket/release/ -r                    upload a directory
   oss cp s3://bucket-a/k.bin s3://bucket-b/k.bin           server-side copy

DIRECTORY LAYOUT:
   With -r, slashes in keys become local directories. When the source ends
   with /, its contents are placed directly into the destination
   (e.g. s3://b/logs/ -> ./logs/<files>).

Flag groups:
   scope: -r/--recursive  --include  --exclude  --skip-existing
   perf:  --jobs (parallel files)  --parallel (parts per file)
   output: --no-progress`),
		Flags:  flags,
		Action: runCP,
	}
}

func runCP(ctx context.Context, c *cli.Command) error {
	args := c.Args().Slice()
	if len(args) != 2 {
		return errors.New(T("用法: oss cp <源> <目标>", "usage: oss cp <src> <dst>"))
	}
	src, dst := args[0], args[1]
	srcRemote, dstRemote := s3x.IsRemote(src), s3x.IsRemote(dst)
	o := connOpts(c)

	switch {
	case srcRemote && dstRemote:
		return cpS3toS3(ctx, c, o, src, dst)
	case srcRemote:
		return cpDownload(ctx, c, o, src, dst)
	case dstRemote:
		return cpUpload(ctx, c, o, src, dst)
	default:
		return errors.New(T(
			"源和目标都是本地路径；远端请带 scheme（s3:// oss:// http(s):// 等）",
			"both src and dst are local paths; remote targets need a scheme (s3:// oss:// http(s):// ...)"))
	}
}

// relToPrefix returns the destination-relative path of key when copying the
// given prefix recursively. The prefix is trimmed only up to its last "/"
// so that a partial prefix like "logs/2026" keeps "2026/..." in the path
// (and "index" does not mangle "index.html" into ".html").
func relToPrefix(key, prefix string) string {
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		return strings.TrimPrefix(key, prefix[:i+1])
	}
	return key
}

// ---------------------------------------------------------------- download

func cpDownload(ctx context.Context, c *cli.Command, o *s3x.ConnOpts, src, dst string) error {
	t, err := s3x.ParseTarget(src, o)
	if err != nil {
		return err
	}
	if t == nil || t.Bucket == "" {
		return errors.New(T("下载来源必须包含桶名", "download source must include a bucket"))
	}
	cl, err := s3x.New(ctx, o, t)
	if err != nil {
		return err
	}

	if !c.Bool("recursive") {
		if t.Key == "" {
			return errors.New(T("需要对象 key；按前缀下载请加 -r", "object key required; add -r to download by prefix"))
		}
		return downloadOne(ctx, c, cl, t, dst)
	}

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	filter := newFilter(c.StringSlice("include"), c.StringSlice("exclude"))
	skipExisting := c.Bool("skip-existing")
	agg := newAggregate(progressEnabled(c), "↓")
	defer agg.Finish()

	dl := manager.NewDownloader(cl.S3, func(d *manager.Downloader) {
		d.Concurrency = max(1, c.Int("parallel"))
	})

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(1, c.Int("jobs")))

	prefix := t.Key
	token := t.List.ContinuationToken
	for {
		if gctx.Err() != nil {
			break
		}
		in := &s3.ListObjectsV2Input{Bucket: aws.String(t.Bucket)}
		if prefix != "" {
			in.Prefix = aws.String(prefix)
		}
		if token != "" {
			in.ContinuationToken = aws.String(token)
		}
		pg, err := s3x.ListV2(gctx, cl.S3, in)
		if err != nil {
			return apiErr(err, cl.Anonymous)
		}

		// Stream objects straight into the bounded worker pool: memory stays
		// flat no matter how many objects the bucket holds.
		for _, obj := range pg.Objects {
			key := aws.ToString(obj.Key)
			rel := relToPrefix(key, prefix)
			if rel == "" {
				continue
			}
			dest := filepath.Join(dst, filepath.FromSlash(rel))
			if strings.HasSuffix(rel, "/") {
				_ = os.MkdirAll(dest, 0o755) // directory marker
				continue
			}
			if !filter.Match(rel) {
				continue
			}
			if skipExisting {
				if _, err := os.Stat(dest); err == nil {
					agg.Skip()
					continue
				}
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			k, d := key, dest
			// g.Go blocks while the limit is reached -> backpressure on the
			// lister, so keys are never buffered unboundedly.
			g.Go(func() error {
				tmp := d + ".part"
				f, err := os.Create(tmp)
				if err != nil {
					return err
				}
				n, err := dl.Download(gctx, f, &s3.GetObjectInput{
					Bucket: aws.String(t.Bucket), Key: aws.String(k),
				})
				_ = f.Close()
				if err != nil {
					_ = os.Remove(tmp)
					return fmt.Errorf("%s: %w", k, err)
				}
				if err := os.Rename(tmp, d); err != nil {
					return err
				}
				agg.Add(1, n)
				return nil
			})
		}
		if !pg.Truncated || pg.NextToken == "" {
			break
		}
		token = pg.NextToken
	}
	if err := g.Wait(); err != nil {
		return apiErr(err, cl.Anonymous)
	}
	return nil
}

func downloadOne(ctx context.Context, c *cli.Command, cl *s3x.Client, t *s3x.Target, dst string) error {
	dl := manager.NewDownloader(cl.S3, func(d *manager.Downloader) {
		d.Concurrency = max(1, c.Int("parallel"))
	})
	in := &s3.GetObjectInput{Bucket: aws.String(t.Bucket), Key: aws.String(t.Key)}

	// Stream to stdout.
	if dst == "-" {
		resp, err := cl.S3.GetObject(ctx, in)
		if err != nil {
			return apiErr(err, cl.Anonymous)
		}
		defer resp.Body.Close()
		_, err = io.Copy(os.Stdout, resp.Body)
		return err
	}

	// A trailing separator means "into this directory" even if it does not
	// exist yet; an existing directory always receives the base name.
	if strings.HasSuffix(dst, "/") || strings.HasSuffix(dst, string(os.PathSeparator)) {
		dst = filepath.Join(dst, path.Base(t.Key))
	} else if fi, err := os.Stat(dst); err == nil && fi.IsDir() {
		dst = filepath.Join(dst, path.Base(t.Key))
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	var pw *progressWriter
	if head, err := cl.S3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: in.Bucket, Key: in.Key,
	}); err == nil && head.ContentLength != nil && *head.ContentLength > 0 && progressEnabled(c) {
		pw = &progressWriter{bar: newBar(*head.ContentLength, "↓ "+path.Base(t.Key))}
	}

	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	var w io.WriterAt = f
	if pw != nil {
		pw.w = f
		w = pw
	}
	n, err := dl.Download(ctx, w, in)
	_ = f.Close()
	if pw != nil {
		pw.bar.Finish()
	}
	if err != nil {
		_ = os.Remove(tmp)
		return apiErr(fmt.Errorf("download %s: %w", t.Key, err), cl.Anonymous)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s %s  %s\n", eCyan("↓"), dst,
		sizeColored(humanSize(n, false), n))
	return nil
}

// ------------------------------------------------------------------ upload

func cpUpload(ctx context.Context, c *cli.Command, o *s3x.ConnOpts, src, dst string) error {
	t, err := s3x.ParseTarget(dst, o)
	if err != nil {
		return err
	}
	if t == nil || t.Bucket == "" {
		return errors.New(T("上传目标必须包含桶名", "upload destination must include a bucket"))
	}
	cl, err := s3x.New(ctx, o, t)
	if err != nil {
		return err
	}
	up := manager.NewUploader(cl.S3, func(u *manager.Uploader) {
		u.Concurrency = max(1, c.Int("parallel"))
	})

	fi, err := os.Stat(src)
	if err != nil {
		return err
	}

	if !c.Bool("recursive") {
		if fi.IsDir() {
			return errors.New(T("源是目录；请加 -r", "source is a directory; add -r"))
		}
		key := t.Key
		if key == "" || strings.HasSuffix(key, "/") {
			key += filepath.Base(src)
		}
		return uploadOne(ctx, c, up, src, t.Bucket, key)
	}
	if !fi.IsDir() {
		return errors.New(T("-r 需要目录作为源", "-r requires a directory source"))
	}

	base := strings.TrimSuffix(t.Key, "/")
	filter := newFilter(c.StringSlice("include"), c.StringSlice("exclude"))
	agg := newAggregate(progressEnabled(c), "↑")
	defer agg.Finish()

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(1, c.Int("jobs")))

	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if gctx.Err() != nil {
			return gctx.Err()
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !filter.Match(rel) {
			return nil
		}
		key := rel
		if base != "" {
			key = base + "/" + rel
		}
		file, bucket := p, t.Bucket
		g.Go(func() error { return putFile(gctx, up, file, bucket, key, agg) })
		return nil
	})
	if err != nil {
		return err
	}
	return apiErr(g.Wait(), cl.Anonymous)
}

func uploadOne(ctx context.Context, c *cli.Command, up *manager.Uploader, src, bucket, key string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}

	var body io.Reader = f
	if progressEnabled(c) && fi.Size() > 0 {
		bar := newBar(fi.Size(), "↑ "+filepath.Base(src))
		body = io.TeeReader(f, bar)
		defer bar.Finish()
	}
	in := &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: body}
	if ct := mime.TypeByExtension(filepath.Ext(src)); ct != "" {
		in.ContentType = aws.String(ct)
	}
	if _, err := up.Upload(ctx, in); err != nil {
		return apiErr(fmt.Errorf("upload %s: %w", key, err), false)
	}
	fmt.Fprintf(os.Stderr, "%s %s  %s\n", eCyan("↑"), key,
		sizeColored(humanSize(fi.Size(), false), fi.Size()))
	return nil
}

func putFile(ctx context.Context, up *manager.Uploader, file, bucket, key string, agg *aggregate) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	in := &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: f}
	if ct := mime.TypeByExtension(filepath.Ext(file)); ct != "" {
		in.ContentType = aws.String(ct)
	}
	if _, err := up.Upload(ctx, in); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	agg.Add(1, fi.Size())
	return nil
}

// ------------------------------------------------------- server-side copy

func cpS3toS3(ctx context.Context, c *cli.Command, o *s3x.ConnOpts, src, dst string) error {
	ts, err := s3x.ParseTarget(src, o)
	if err != nil {
		return err
	}
	td, err := s3x.ParseTarget(dst, o)
	if err != nil {
		return err
	}
	if ts == nil || td == nil || ts.Bucket == "" || td.Bucket == "" {
		return errors.New(T("源和目标都必须包含桶名", "both src and dst must include a bucket"))
	}
	es, _, _, _, err := s3x.Resolve(o, ts)
	if err != nil {
		return err
	}
	ed, _, _, _, err := s3x.Resolve(o, td)
	if err != nil {
		return err
	}
	if es != ed {
		return fmt.Errorf(T(
			"不支持跨 endpoint 拷贝（%s 与 %s）；请先下载到本地再上传",
			"cross-endpoint copy is not supported (%s vs %s); download locally, then upload"), es, ed)
	}
	cl, err := s3x.New(ctx, o, ts)
	if err != nil {
		return err
	}

	copyOne := func(srcKey, dstKey string) error {
		_, err := cl.S3.CopyObject(ctx, &s3.CopyObjectInput{
			Bucket:     aws.String(td.Bucket),
			Key:        aws.String(dstKey),
			CopySource: aws.String(ts.Bucket + "/" + url.PathEscape(srcKey)),
		})
		if err != nil {
			return fmt.Errorf("%s -> %s: %w", srcKey, dstKey, err)
		}
		return nil
	}

	if !c.Bool("recursive") {
		if ts.Key == "" {
			return errors.New(T("需要对象 key；按前缀拷贝请加 -r", "object key required; add -r to copy by prefix"))
		}
		dstKey := td.Key
		if dstKey == "" || strings.HasSuffix(dstKey, "/") {
			dstKey += path.Base(ts.Key)
		}
		if err := copyOne(ts.Key, dstKey); err != nil {
			return apiErr(err, cl.Anonymous)
		}
		fmt.Fprintf(os.Stderr, "%s %s/%s %s %s/%s\n", eCyan("⇄"),
			ts.Bucket, ts.Key, eCyan("→"), td.Bucket, dstKey)
		return nil
	}

	agg := newAggregate(progressEnabled(c), "⇄")
	defer agg.Finish()
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(1, c.Int("jobs")))

	prefix := ts.Key
	base := strings.TrimSuffix(td.Key, "/")
	token := ts.List.ContinuationToken
	for {
		if gctx.Err() != nil {
			break
		}
		in := &s3.ListObjectsV2Input{Bucket: aws.String(ts.Bucket)}
		if prefix != "" {
			in.Prefix = aws.String(prefix)
		}
		if token != "" {
			in.ContinuationToken = aws.String(token)
		}
		pg, err := s3x.ListV2(gctx, cl.S3, in)
		if err != nil {
			return apiErr(err, cl.Anonymous)
		}
		for _, obj := range pg.Objects {
			key := aws.ToString(obj.Key)
			if strings.HasSuffix(key, "/") {
				continue
			}
			rel := relToPrefix(key, prefix)
			dstKey := rel
			if base != "" {
				dstKey = base + "/" + rel
			}
			k, dk := key, dstKey
			g.Go(func() error {
				if err := copyOne(k, dk); err != nil {
					return err
				}
				var n int64
				if obj.Size != nil {
					n = *obj.Size
				}
				agg.Add(1, n)
				return nil
			})
		}
		if !pg.Truncated || pg.NextToken == "" {
			break
		}
		token = pg.NextToken
	}
	return apiErr(g.Wait(), cl.Anonymous)
}
