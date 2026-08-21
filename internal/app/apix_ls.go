package app

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	errs "github.com/ejfkdev/xyz-go/errors"
	"github.com/ejfkdev/xyz-go/registry"
	"github.com/ejfkdev/xyz-go/spec"

	"github.com/ejfkdev/oss/internal/s3x"
)

// apixLsArgs lists a bucket, a prefix window or all visible buckets.
// Connection fields follow the canonical set documented in apix.go.
type apixLsArgs struct {
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

	Target     string   `json:"target,omitempty" desc:"桶或前缀目标（s3://… 或 URL；留空则列出桶）/ bucket or prefix target; omit to list buckets"`
	Prefix     string   `json:"prefix,omitempty" desc:"键前缀 / key prefix"`
	Delimiter  *string  `json:"delimiter,omitempty" desc:"目录分隔符（默认 /；空串递归平铺）/ delimiter"`
	Recursive  bool     `json:"recursive,omitempty" desc:"递归平铺前缀 / recursive flat listing"`
	Dirs       bool     `json:"dirs,omitempty" desc:"只列目录 / directories only"`
	Files      bool     `json:"files,omitempty" desc:"只列文件 / files only"`
	Include    []string `json:"include,omitempty" desc:"包含 glob（可多个）/ include globs"`
	Exclude    []string `json:"exclude,omitempty" desc:"排除 glob（可多个）/ exclude globs"`
	Limit      int64    `json:"limit,omitempty" desc:"本次最多返回条目数 / max entries" default:"1000"`
	All        bool     `json:"all,omitempty" desc:"返回全部匹配条目（不受 limit 限制）/ fetch everything"`
	PageSize   int64    `json:"page_size,omitempty" desc:"每页请求大小 / server page size"`
	NextToken  string   `json:"next_token,omitempty" desc:"分页续传令牌 / pagination token"`
	StartAfter string   `json:"start_after,omitempty" desc:"从此键之后开始 / start after key"`
}

// apixLsEntry is one listing entry: a bucket, a common prefix or an object.
type apixLsEntry struct {
	Type         string     `json:"type"` // bucket | prefix | object
	Key          string     `json:"key,omitempty"`
	Name         string     `json:"name,omitempty"`
	Size         *int64     `json:"size,omitempty"`
	LastModified *time.Time `json:"last_modified,omitempty"`
	ETag         string     `json:"etag,omitempty"`
	StorageClass string     `json:"storage_class,omitempty"`
	CreationDate *time.Time `json:"creation_date,omitempty"`
}

type apixLsResp struct {
	Target    string        `json:"target,omitempty"`
	Entries   []apixLsEntry `json:"entries"`
	Shown     int           `json:"shown"`
	NextToken string        `json:"next_token,omitempty"`
	Truncated bool          `json:"truncated"`
}

func registerApixLs(reg *registry.Registry) error {
	_, err := spec.Define("ls", apixLs).
		Summary("列出桶、目录前缀或对象 / list buckets, prefixes or objects").
		Description("按目标（s3://… 或 URL）列出条目：留空 target 列出桶；给定桶或前缀时返回一页条目（默认 1000 条），用 next_token 续传；all=true 返回全部。\n\nLists entries for a target: bucket listing when target is empty; one page of entries (1000 by default) with next_token pagination otherwise; all=true fetches everything.").
		HTTP(xyzHintsGET("/ls")).
		MCP(xyzMCPRead()).
		Register(reg)
	return err
}

func apixLs(ctx context.Context, in *apixLsArgs) (*apixLsResp, error) {
	if in.Dirs && in.Files {
		return nil, errs.New(errs.KindInvalidInput, T("--dirs 与 --files 不能同时使用", "--dirs and --files cannot be used together"))
	}
	if in.Limit <= 0 && !in.All {
		return nil, errs.New(errs.KindInvalidInput, T("limit 必须大于 0", "limit must be greater than 0"))
	}
	o := connOptsFrom(in.AK, in.SK, in.Token, in.Profile, in.Provider, in.Endpoint, in.Region,
		in.Proxy, in.Bucket, in.Anonymous, in.PathStyle, in.Insecure, in.Headers, in.Timeout)
	t, err := s3x.ParseTarget(in.Target, o)
	if err != nil {
		return nil, apixErr(false, err)
	}
	cl, err := s3x.New(ctx, o, t)
	if err != nil {
		return nil, apixErr(false, err)
	}
	resp := &apixLsResp{Target: in.Target}

	// No target at all: list the buckets visible to the connection.
	if t == nil {
		out, err := cl.S3.ListBuckets(ctx, &s3.ListBucketsInput{})
		if err != nil {
			return nil, apixErr(cl.Anonymous, err)
		}
		for _, b := range out.Buckets {
			resp.Entries = append(resp.Entries, apixLsEntry{
				Type: "bucket", Name: aws.ToString(b.Name), CreationDate: b.CreationDate,
			})
		}
		resp.Shown = len(resp.Entries)
		return resp, nil
	}
	if t.Bucket == "" {
		return nil, errs.New(errs.KindInvalidInput, T("无法从目标解析出桶名", "cannot determine bucket name from target"))
	}

	f := listFlags{
		recursive:  in.Recursive,
		prefix:     in.Prefix,
		startAfter: in.StartAfter,
		dirsOnly:   in.Dirs,
		filesOnly:  in.Files,
		include:    in.Include,
		exclude:    in.Exclude,
	}
	if in.Delimiter != nil {
		f.delimiter = in.Delimiter
	}
	if in.PageSize > 0 {
		f.pageSize = int64Ptr(in.PageSize)
	}
	p := resolveListParamsF(f, t)
	ef := newEntryFilterF(f, p)

	entries, nextToken, truncated, err := listWindow(ctx, cl, t, p, ef, in.Limit, in.All, in.NextToken)
	if err != nil {
		return nil, apixErr(cl.Anonymous, err)
	}
	for _, e := range entries {
		resp.Entries = append(resp.Entries, entryToApix(e))
	}
	resp.Shown = len(resp.Entries)
	resp.NextToken = nextToken
	resp.Truncated = truncated
	return resp, nil
}

// entryToApix maps an internal cached entry onto the API shape.
func entryToApix(e cachedEntry) apixLsEntry {
	if e.Dir {
		return apixLsEntry{Type: "prefix", Key: e.Key}
	}
	ent := apixLsEntry{
		Type: "object", Key: e.Key, Size: &e.Size,
		ETag: strings.Trim(e.ETag, `"`), StorageClass: e.SC,
	}
	if e.Mod > 0 {
		mod := time.Unix(e.Mod, 0)
		ent.LastModified = &mod
	}
	return ent
}
