package s3x

import (
	"strings"
	"testing"
)

func TestScanURLs(t *testing.T) {
	cases := []struct {
		probe    ScanProbe
		bucket   string
		regions  []string
		wantURLs []string
	}{
		{
			probe:    ScanProbe{URLTemplate: "https://{bucket}.s3.amazonaws.com"},
			bucket:   "mybucket",
			regions:  nil, // region-less probe
			wantURLs: []string{"https://mybucket.s3.amazonaws.com"},
		},
		{
			probe:    ScanProbe{URLTemplate: "https://{bucket}.oss-{region}.aliyuncs.com"},
			bucket:   "mybucket",
			regions:  []string{"cn-hangzhou", "cn-beijing"},
			wantURLs: []string{"https://mybucket.oss-cn-hangzhou.aliyuncs.com", "https://mybucket.oss-cn-beijing.aliyuncs.com"},
		},
		{
			probe:    ScanProbe{URLTemplate: "https://{bucket}.oss-{region}.aliyuncs.com"},
			bucket:   "mybucket",
			regions:  []string{"cn-shenzhen"}, // single region override
			wantURLs: []string{"https://mybucket.oss-cn-shenzhen.aliyuncs.com"},
		},
		{
			probe:    ScanProbe{URLTemplate: "https://s3.{region}.jdcloud-oss.com/{bucket}"},
			bucket:   "mybucket",
			regions:  []string{"cn-north-1"},
			wantURLs: []string{"https://s3.cn-north-1.jdcloud-oss.com/mybucket"},
		},
	}
	for _, tc := range cases {
		got := tc.probe.ScanURLs(tc.bucket, tc.regions)
		if len(got) != len(tc.wantURLs) {
			t.Fatalf("ScanURLs(%q, %v) = %v, want %v", tc.bucket, tc.regions, got, tc.wantURLs)
		}
		for i := range got {
			if got[i] != tc.wantURLs[i] {
				t.Errorf("ScanURLs(%q, %v)[%d] = %q, want %q", tc.bucket, tc.regions, i, got[i], tc.wantURLs[i])
			}
		}
	}
}

func TestNewProviderTemplates(t *testing.T) {
	cases := []struct {
		provider   string
		region     string
		wantSuffix string
	}{
		{"ucloud", "cn-sh2", "https://s3-cn-sh2.ufileos.com"},
		{"jdcloud", "cn-north-1", "https://s3.cn-north-1.jdcloud-oss.com"},
		{"ks3", "cn-beijing", "https://ks3-cn-beijing.ksyuncs.com"},
		{"baidu", "bj", "https://s3.bj.bcebos.com"},
		{"scaleway", "fr-par", "https://s3.fr-par.scw.cloud"},
	}
	for _, tc := range cases {
		p, ok := Providers[tc.provider]
		if !ok {
			t.Fatalf("provider %q not registered", tc.provider)
		}
		if got := renderEndpoint(p.EndpointTemplate, tc.region); got != tc.wantSuffix {
			t.Errorf("endpoint for %s/%s = %q, want %q", tc.provider, tc.region, got, tc.wantSuffix)
		}
	}
}

func TestParseNewProviderHosts(t *testing.T) {
	cases := []struct {
		url          string
		wantProvider string
		wantBucket   string
		wantRegion   string
	}{
		{"https://mybucket.s3-cn-sh2.ufileos.com/k", "ucloud", "mybucket", "cn-sh2"},
		{"https://s3.cn-north-1.jdcloud-oss.com/mybucket/k", "jdcloud", "mybucket", "cn-north-1"},
		{"https://mybucket.ks3-cn-beijing.ksyuncs.com/k", "ks3", "mybucket", "cn-beijing"},
		{"https://mybucket.s3.bj.bcebos.com/k", "baidu", "mybucket", "bj"},
		{"https://s3.bj.bcebos.com/mybucket/k", "baidu", "mybucket", "bj"},
		{"https://mybucket.s3.fr-par.scw.cloud/k", "scaleway", "mybucket", "fr-par"},
		// UCloud standard UFile virtual-host format {bucket}.{region}.ufileos.com
		{"https://maas-watermark-prod-new.cn-wlcb.ufileos.com/", "ucloud", "maas-watermark-prod-new", "cn-wlcb"},
		// UCloud S3-compatible format {bucket}.s3-{region}.ufileos.com
		{"https://mybucket.s3-cn-sh2.ufileos.com/k", "ucloud", "mybucket", "cn-sh2"},
	}
	for _, tc := range cases {
		tgt, err := ParseTarget(tc.url, &ConnOpts{})
		if err != nil {
			t.Fatalf("ParseTarget(%q): %v", tc.url, err)
		}
		if tgt.Provider != tc.wantProvider || tgt.Bucket != tc.wantBucket || tgt.Region != tc.wantRegion {
			t.Errorf("ParseTarget(%q) = provider %q bucket %q region %q, want %q %q %q",
				tc.url, tgt.Provider, tgt.Bucket, tgt.Region, tc.wantProvider, tc.wantBucket, tc.wantRegion)
		}
	}
}

func TestValidBucketName(t *testing.T) {
	valid := []string{"mybucket", "my-bucket", "abc", "noaa-nwm-pds", "bucket.example.com"}
	invalid := []string{"", "a", "-abc", "abc-", "ab_cd", strings.Repeat("x", 64)}
	for _, s := range valid {
		if !ValidBucketName(s) {
			t.Errorf("ValidBucketName(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if ValidBucketName(s) {
			t.Errorf("ValidBucketName(%q) = true, want false", s)
		}
	}
}
