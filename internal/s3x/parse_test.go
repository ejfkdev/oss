package s3x

import (
	"net/url"
	"reflect"
	"testing"
)

func TestParseTargetEmpty(t *testing.T) {
	got, err := ParseTarget("", &ConnOpts{})
	if err != nil || got != nil {
		t.Fatalf("want nil target, got %v, err %v", got, err)
	}
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		name string
		in   string
		opts ConnOpts
		want Target
	}{
		{
			name: "s3 scheme",
			in:   "s3://bucket/a/b?prefix=c",
			want: Target{Provider: "aws", Bucket: "bucket", Key: "a/b", List: ListOptions{Prefix: "c"}},
		},
		{
			name: "oss scheme implies aliyun",
			in:   "oss://bucket2/k",
			want: Target{Provider: "aliyun", Bucket: "bucket2", Key: "k"},
		},
		{
			name: "aws vhost with region",
			in:   "https://mybucket.s3.us-east-1.amazonaws.com/logs/",
			want: Target{Provider: "aws", Endpoint: "https://s3.us-east-1.amazonaws.com",
				Bucket: "mybucket", Key: "logs/", Region: "us-east-1", FromURL: true},
		},
		{
			name: "aws vhost global endpoint",
			in:   "https://mybucket.s3.amazonaws.com/x",
			want: Target{Provider: "aws", Endpoint: "https://s3.us-east-1.amazonaws.com",
				Bucket: "mybucket", Key: "x", Region: "us-east-1", FromURL: true},
		},
		{
			name: "aws vhost dash region",
			in:   "https://mybucket.s3-us-west-2.amazonaws.com/x",
			want: Target{Provider: "aws", Endpoint: "https://s3.us-west-2.amazonaws.com",
				Bucket: "mybucket", Key: "x", Region: "us-west-2", FromURL: true},
		},
		{
			name: "aws path style",
			in:   "https://s3.us-west-2.amazonaws.com/buck/key",
			want: Target{Provider: "aws", Endpoint: "https://s3.us-west-2.amazonaws.com",
				Bucket: "buck", Key: "key", Region: "us-west-2", PathStyle: true, FromURL: true},
		},
		{
			name: "aliyun vhost",
			in:   "https://bkt.oss-cn-hangzhou.aliyuncs.com/dir/",
			want: Target{Provider: "aliyun", Endpoint: "https://oss-cn-hangzhou.aliyuncs.com",
				Bucket: "bkt", Key: "dir/", Region: "cn-hangzhou", FromURL: true},
		},
		{
			name: "aliyun accelerate",
			in:   "https://bkt.oss-accelerate.aliyuncs.com/k",
			want: Target{Provider: "aliyun", Endpoint: "https://oss-accelerate.aliyuncs.com",
				Bucket: "bkt", Key: "k", Region: "cn-hangzhou", FromURL: true},
		},
		{
			name: "tencent cos",
			in:   "https://b-1250000000.cos.ap-guangzhou.myqcloud.com/p",
			want: Target{Provider: "tencent", Endpoint: "https://cos.ap-guangzhou.myqcloud.com",
				Bucket: "b-1250000000", Key: "p", Region: "ap-guangzhou", FromURL: true},
		},
		{
			name: "huawei obs",
			in:   "https://bkt.obs.cn-north-4.myhuaweicloud.com/k",
			want: Target{Provider: "huawei", Endpoint: "https://obs.cn-north-4.myhuaweicloud.com",
				Bucket: "bkt", Key: "k", Region: "cn-north-4", FromURL: true},
		},
		{
			name: "qiniu kodo",
			in:   "https://bkt.s3-cn-east-1.qiniucs.com/k",
			want: Target{Provider: "qiniu", Endpoint: "https://s3-cn-east-1.qiniucs.com",
				Bucket: "bkt", Key: "k", Region: "cn-east-1", FromURL: true},
		},
		{
			name: "cloudflare r2",
			in:   "https://bkt.0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com/k",
			want: Target{Provider: "r2", Endpoint: "https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com",
				Bucket: "bkt", Key: "k", Region: "auto", FromURL: true},
		},
		{
			name: "minio ip path style",
			in:   "https://127.0.0.1:9000/bucket/key",
			want: Target{Endpoint: "https://127.0.0.1:9000", Bucket: "bucket", Key: "key",
				PathStyle: true, FromURL: true},
		},
		{
			name: "unknown host with bucket flag",
			in:   "https://cdn.example.com/a/b",
			opts: ConnOpts{Bucket: "bkt"},
			want: Target{Endpoint: "https://cdn.example.com", Bucket: "bkt", Key: "a/b",
				PathStyle: true, FromURL: true},
		},
		{
			name: "unknown host auto path style (path-forwarded bucket)",
			in:   "https://files.example.com/store-bucket/sub/key.jpg",
			want: Target{Endpoint: "https://files.example.com", Bucket: "store-bucket",
				Key: "sub/key.jpg", PathStyle: true, FromURL: true},
		},
		{
			name: "extra auth query params preserved",
			in:   "http://xxx.com/bucket?token=abc",
			want: Target{Endpoint: "http://xxx.com", Bucket: "bucket", PathStyle: true,
				FromURL: true, ExtraQuery: url.Values{"token": []string{"abc"}}},
		},
		{
			name: "bare bucket",
			in:   "mybucket/prefix/x",
			want: Target{Bucket: "mybucket", Key: "prefix/x"},
		},
		{
			name: "query list options",
			in:   "https://b.s3.amazonaws.com/?prefix=logs/&delimiter=/&max-keys=10&continuation-token=abc&start-after=s&token=zz&sig=xy",
			want: Target{Provider: "aws", Endpoint: "https://s3.us-east-1.amazonaws.com",
				Bucket: "b", Region: "us-east-1", FromURL: true,
				List: ListOptions{
					Prefix:            "logs/",
					Delimiter:         strPtr("/"),
					MaxKeys:           10,
					ContinuationToken: "abc",
					StartAfter:        "s",
				},
				ExtraQuery: url.Values{"token": []string{"zz"}, "sig": []string{"xy"}}},
		},
		{
			name: "empty delimiter in query",
			in:   "https://b.s3.amazonaws.com/?delimiter=",
			want: Target{Provider: "aws", Endpoint: "https://s3.us-east-1.amazonaws.com",
				Bucket: "b", Region: "us-east-1", FromURL: true,
				List: ListOptions{Delimiter: strPtr("")}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTarget(tc.in, &tc.opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == nil {
				t.Fatal("got nil target")
			}
			if got.Provider != tc.want.Provider || got.Endpoint != tc.want.Endpoint ||
				got.Region != tc.want.Region || got.Bucket != tc.want.Bucket ||
				got.Key != tc.want.Key || got.PathStyle != tc.want.PathStyle ||
				got.FromURL != tc.want.FromURL {
				t.Errorf("target mismatch:\n got %+v\nwant %+v", *got, tc.want)
			}
			if got.List.Prefix != tc.want.List.Prefix ||
				got.List.MaxKeys != tc.want.List.MaxKeys ||
				got.List.ContinuationToken != tc.want.List.ContinuationToken ||
				got.List.Marker != tc.want.List.Marker ||
				got.List.StartAfter != tc.want.List.StartAfter {
				t.Errorf("list options mismatch:\n got %+v\nwant %+v", got.List, tc.want.List)
			}
			if (got.List.Delimiter == nil) != (tc.want.List.Delimiter == nil) ||
				(got.List.Delimiter != nil && *got.List.Delimiter != *tc.want.List.Delimiter) {
				t.Errorf("delimiter mismatch: got %v want %v", got.List.Delimiter, tc.want.List.Delimiter)
			}
			if len(got.ExtraQuery) == 0 && len(tc.want.ExtraQuery) == 0 {
				return
			}
			if !reflect.DeepEqual(got.ExtraQuery, tc.want.ExtraQuery) {
				t.Errorf("extra query mismatch:\n got %+v\nwant %+v", got.ExtraQuery, tc.want.ExtraQuery)
			}
		})
	}
}

func TestParseTargetErrors(t *testing.T) {
	bad := []string{
		"https://cdn.example.com",    // unknown host, no path, no --bucket
		"ftp://bucket/key",           // unsupported scheme
		"!!invalid!!bucket/x",        // invalid bare bucket
		"oss://",                     // missing bucket
	}
	for _, in := range bad {
		if _, err := ParseTarget(in, &ConnOpts{}); err == nil {
			t.Errorf("expected error for %q", in)
		}
	}
}

func TestResolve(t *testing.T) {
	// aliyun via provider flag
	ep, region, _, provider, err := Resolve(&ConnOpts{Provider: "aliyun"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ep != "https://s3.cn-hangzhou.oss.aliyuncs.com" || region != "cn-hangzhou" || provider != "aliyun" {
		t.Errorf("got %s %s %s", ep, region, provider)
	}

	// region override renders endpoint
	ep, region, _, _, err = Resolve(&ConnOpts{Provider: "tencent", Region: "ap-shanghai"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ep != "https://cos.ap-shanghai.myqcloud.com" || region != "ap-shanghai" {
		t.Errorf("got %s %s", ep, region)
	}

	// endpoint flag wins over template
	ep, _, _, _, err = Resolve(&ConnOpts{Provider: "aws", Endpoint: "http://127.0.0.1:9000/"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ep != "http://127.0.0.1:9000" {
		t.Errorf("got %s", ep)
	}

	// minio requires endpoint
	if _, _, _, _, err = Resolve(&ConnOpts{Provider: "minio"}, nil); err == nil {
		t.Error("expected error for minio without endpoint")
	}

	// unknown provider
	if _, _, _, _, err = Resolve(&ConnOpts{Provider: "doesnotexist"}, nil); err == nil {
		t.Error("expected error for unknown provider")
	}

	// gcs forces path style
	_, _, pathStyle, _, err := Resolve(&ConnOpts{Provider: "gcs"}, nil)
	if err != nil || !pathStyle {
		t.Errorf("gcs should force path style, pathStyle=%v err=%v", pathStyle, err)
	}
}

func TestParseHeaders(t *testing.T) {
	h := ParseHeaders([]string{
		"User-Agent: my-agent/1.0",
		"Cookie: a=b; c=d",
		"ignored-no-colon",
		" X-Custom : spaced ",
	})
	if h["User-Agent"] != "my-agent/1.0" || h["Cookie"] != "a=b; c=d" || h["X-Custom"] != "spaced" {
		t.Errorf("unexpected headers: %+v", h)
	}
	if _, ok := h["ignored-no-colon"]; ok {
		t.Error("invalid header should be skipped")
	}
}

func strPtr(s string) *string { return &s }
