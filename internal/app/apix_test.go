package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	errs "github.com/ejfkdev/xyz-go/errors"
	"github.com/ejfkdev/xyz-go/httpapi"
)

// fakeAPIErr mimics a smithy error carrying an error code.
type fakeAPIErr struct{ code string }

func (e *fakeAPIErr) Error() string     { return "api error " + e.code }
func (e *fakeAPIErr) ErrorCode() string { return e.code }

func TestApixClassify(t *testing.T) {
	cases := []struct {
		err  error
		want errs.Kind
	}{
		// xyz-go coded errors win.
		{errs.New(errs.KindUnauthorized, "need credentials"), errs.KindUnauthorized},
		{errs.New(errs.KindNotFound, "gone"), errs.KindNotFound},
		// smithy-style codes.
		{&fakeAPIErr{"AccessDenied"}, errs.KindForbidden},
		{&fakeAPIErr{"InvalidAccessKeyId"}, errs.KindUnauthorized},
		{&fakeAPIErr{"SignatureDoesNotMatch"}, errs.KindUnauthorized},
		{&fakeAPIErr{"NoSuchBucket"}, errs.KindNotFound},
		{&fakeAPIErr{"NoSuchKey"}, errs.KindNotFound},
		{&fakeAPIErr{"MethodNotAllowed"}, errs.KindInvalidInput},
		{&fakeAPIErr{"SomeUnknownCode"}, errs.KindInternal},
		// transport-level.
		{errors.New("connection refused"), errs.KindUnavailable},
		{context.DeadlineExceeded, errs.KindUnavailable},
		// usage text.
		{errors.New("用法: oss stat <桶[/对象]>"), errs.KindInvalidInput},
		{errors.New("something else"), errs.KindInternal},
	}
	for _, c := range cases {
		if got := apixClassify(c.err); got != c.want {
			t.Errorf("apixClassify(%v) = %q, want %q", c.err, got, c.want)
		}
	}
	if got := apixClassify(nil); got != errs.KindInternal {
		t.Errorf("apixClassify(nil) = %q, want internal", got)
	}
}

func TestApixErrKeepsMessage(t *testing.T) {
	raw := &fakeAPIErr{"NoSuchBucket"}
	got := apixErr(false, raw)
	if apixClassify(got) != errs.KindNotFound {
		t.Errorf("kind = %q, want not_found", apixClassify(got))
	}
	if errs.Cause(got) == nil {
		t.Error("cause should be preserved")
	}
}

func TestBuildAPIRegistry(t *testing.T) {
	reg, err := BuildAPIRegistry()
	if err != nil {
		t.Fatalf("BuildAPIRegistry: %v", err)
	}
	for _, name := range []string{"ls", "stat", "presign", "find"} {
		if e, ok := reg.Get(name); !ok || e == nil {
			t.Errorf("command %q not registered", name)
		}
	}
}

func TestAPIRegistryHTTPSmoke(t *testing.T) {
	reg, err := BuildAPIRegistry()
	if err != nil {
		t.Fatalf("BuildAPIRegistry: %v", err)
	}
	h, err := httpapi.Handler(reg)
	if err != nil {
		t.Fatalf("httpapi.Handler: %v", err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	// healthz works without any backend.
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz = %d, want 200", resp.StatusCode)
	}

	// openapi.json is served and mentions the registered routes.
	resp2, err := http.Get(srv.URL + "/openapi.json")
	if err != nil {
		t.Fatalf("GET /openapi.json: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("/openapi.json = %d, want 200", resp2.StatusCode)
	}

	// An unknown route answers 404.
	resp3, err := http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatalf("GET /nope: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Errorf("/nope = %d, want 404", resp3.StatusCode)
	}
}

func TestEntryToApix(t *testing.T) {
	dir := entryToApix(cachedEntry{Dir: true, Key: "logs/"})
	if dir.Type != "prefix" || dir.Key != "logs/" {
		t.Errorf("dir entry = %+v", dir)
	}
	obj := entryToApix(cachedEntry{Key: "a.txt", Size: 42, ETag: `"abc"`, SC: "STANDARD", Mod: 1700000000})
	if obj.Type != "object" || *obj.Size != 42 || obj.ETag != "abc" || obj.StorageClass != "STANDARD" {
		t.Errorf("object entry = %+v", obj)
	}
	if obj.LastModified == nil || obj.LastModified.Unix() != 1700000000 {
		t.Errorf("last modified = %v", obj.LastModified)
	}
	zero := entryToApix(cachedEntry{Key: "b.txt"})
	if zero.LastModified != nil {
		t.Errorf("zero mod should yield nil time, got %v", zero.LastModified)
	}
}

func TestConnOptsFrom(t *testing.T) {
	o := connOptsFrom("ak", "sk", "tok", "prof", "aliyun", "https://e", "r", "http://p",
		"b", true, true, true, []string{"A: B"}, 3*time.Second)
	if o.AK != "ak" || o.SK != "sk" || o.Token != "tok" || o.Profile != "prof" ||
		o.Provider != "aliyun" || o.Endpoint != "https://e" || o.Region != "r" ||
		o.Proxy != "http://p" || o.Bucket != "b" || !o.Anonymous || !o.PathStyle ||
		!o.Insecure || len(o.Headers) != 1 || o.Timeout != 3*time.Second {
		t.Errorf("connOptsFrom = %+v", o)
	}
}
