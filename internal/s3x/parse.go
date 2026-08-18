package s3x

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ListOptions are list parameters carried by a target, typically taken from
// the URL query string (prefix, delimiter, max-keys, continuation-token,
// marker, start-after) — the same parameters aws-s3-bucket-browser uses.
type ListOptions struct {
	Prefix            string
	Delimiter         *string // nil = unspecified
	MaxKeys           int32
	ContinuationToken string
	Marker            string
	StartAfter        string
}

// Target is a parsed object-storage location.
type Target struct {
	Provider  string // implied provider, "" = derive from flags
	Endpoint  string // "" = derive from provider/flags
	Region    string // "" = derive
	Bucket    string
	Key       string // object key or key prefix
	PathStyle bool
	FromURL   bool
	List      ListOptions
	// ExtraQuery holds URL query parameters that are not S3 list-API
	// parameters (e.g. ?token=abc auth gateways). They are injected into
	// every request sent to the service.
	ExtraQuery url.Values
}

// schemeProviders maps URL schemes to implied providers.
var schemeProviders = map[string]string{
	"s3":  "aws",
	"s3n": "aws",
	"s3a": "aws",
	"oss": "aliyun",
	"cos": "tencent",
	"obs": "huawei",
}

var (
	reAWSEndpoint    = regexp.MustCompile(`^s3(?:[.-]([a-z0-9][a-z0-9-]*))?\.amazonaws\.com$`)
	reAWSVHost       = regexp.MustCompile(`^(.+)\.s3(?:[.-]([a-z0-9][a-z0-9-]*))?\.amazonaws\.com$`)
	reAliVHost       = regexp.MustCompile(`^(.+)\.(oss-[a-z0-9-]+)\.aliyuncs\.com$`)
	reAliEndpoint    = regexp.MustCompile(`^(oss-[a-z0-9-]+)\.aliyuncs\.com$`)
	reTencentHost    = regexp.MustCompile(`^(.+)\.(cos(?:\.[a-z0-9-]+)+)\.myqcloud\.com$`)
	reHuaweiVHost    = regexp.MustCompile(`^(.+)\.obs\.([a-z0-9-]+)\.myhuaweicloud\.com$`)
	reHuaweiEndpoint = regexp.MustCompile(`^obs\.([a-z0-9-]+)\.myhuaweicloud\.com$`)
	reQiniuVHost     = regexp.MustCompile(`^(.+)\.(s3-[a-z0-9-]+)\.qiniucs\.com$`)
	reR2VHost        = regexp.MustCompile(`^(.+)\.([a-z0-9]{32})\.r2\.cloudflarestorage\.com$`)
	reGCSEndpoint    = regexp.MustCompile(`^storage\.googleapis\.com$`)
	reGCSVHost       = regexp.MustCompile(`^(.+)\.storage\.googleapis\.com$`)

	// smaller / regional providers
	reUCloudVHost     = regexp.MustCompile(`^(.+)\.((?:s3-)?[a-z0-9][a-z0-9-]*)\.ufileos\.com$`)
	reJDCloudEndpoint = regexp.MustCompile(`^s3\.([a-z0-9-]+)\.jdcloud-oss\.com$`)
	reKS3VHost        = regexp.MustCompile(`^(.+)\.(ks3-[a-z0-9-]+)\.ksyuncs\.com$`)
	reBaiduVHost      = regexp.MustCompile(`^(.+)\.s3\.([a-z0-9-]+)\.bcebos\.com$`)
	reBaiduEndpoint   = regexp.MustCompile(`^s3\.([a-z0-9-]+)\.bcebos\.com$`)
	reScalewayVHost   = regexp.MustCompile(`^(.+)\.s3\.([a-z0-9-]+)\.scw\.cloud$`)
	reScalewayEndpt   = regexp.MustCompile(`^s3\.([a-z0-9-]+)\.scw\.cloud$`)

	reBucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
)

// ValidBucketName reports whether name is a syntactically valid bucket name.
func ValidBucketName(name string) bool {
	return reBucketName.MatchString(name)
}

// BucketRootURL reconstructs the bucket root URL from a parsed target
// (scheme://bucket.host/ for virtual-host style, endpoint/bucket/ for
// path-style). Returns "" when the target carries no endpoint.
func BucketRootURL(t *Target) string {
	if t == nil || t.Endpoint == "" || t.Bucket == "" {
		return ""
	}
	u, err := url.Parse(t.Endpoint)
	if err != nil || u.Host == "" {
		return ""
	}
	if t.PathStyle {
		return u.Scheme + "://" + u.Host + "/" + t.Bucket + "/"
	}
	return u.Scheme + "://" + t.Bucket + "." + u.Host + "/"
}

// IsRemote reports whether s looks like a remote object-storage reference
// (it carries a scheme we understand).
func IsRemote(s string) bool {	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" {
		return false
	}
	switch u.Scheme {
	case "http", "https", "s3", "s3n", "s3a", "oss", "cos", "obs":
		return true
	}
	return false
}

// ParseTarget turns user input into a Target.
//
// Accepted forms:
//   - ""                                   -> nil (meaning: list buckets)
//   - s3://bucket/key, oss://bucket/key... -> scheme style
//   - https://bucket.s3.region.amazonaws.com/key?prefix=...
//   - mybucket[/prefix]                    -> bare bucket (needs provider flags)
//
// URL query parameters understood for listing:
// prefix, delimiter, max-keys, continuation-token (or token), marker,
// start-after, encoding-type (ignored).
func ParseTarget(input string, o *ConnOpts) (*Target, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, nil
	}

	if u, err := url.Parse(input); err == nil && u.Scheme != "" {
		switch u.Scheme {
		case "http", "https":
			return parseHTTP(u, o)
		default:
			if provider, ok := schemeProviders[u.Scheme]; ok {
				if u.Host == "" {
					return nil, fmt.Errorf("missing bucket in %q", input)
				}
				t := &Target{
					Provider: provider,
					Bucket:   u.Host,
					Key:      strings.TrimPrefix(u.Path, "/"),
				}
				applyQuery(t, u.Query())
				return t, nil
			}
			return nil, fmt.Errorf("unsupported scheme %q (want s3://, oss://, cos://, obs:// or http(s)://)", u.Scheme)
		}
	}

	if strings.Contains(input, "://") {
		return nil, fmt.Errorf("invalid target %q", input)
	}

	// Bare "bucket[/prefix]".
	bucket, key, _ := strings.Cut(input, "/")
	if !reBucketName.MatchString(bucket) {
		return nil, fmt.Errorf("invalid bucket name %q in target %q", bucket, input)
	}
	return &Target{Bucket: bucket, Key: key}, nil
}

func parseHTTP(u *url.URL, o *ConnOpts) (*Target, error) {
	host := u.Hostname()
	key := strings.TrimPrefix(u.Path, "/")

	t := &Target{FromURL: true, Key: key}
	applyQuery(t, u.Query())

	if provider, endpoint, bucket, region, ok := matchKnownHost(host); ok {
		t.Provider = provider
		t.Endpoint = endpoint
		t.Region = region
		if bucket != "" {
			t.Bucket = bucket
		} else {
			// Path-style service endpoint: bucket is the first path segment.
			t.Bucket, t.Key = splitFirstSegment(t.Key)
			t.PathStyle = true
		}
	} else {
		// Unknown host: MinIO / Ceph / path-forwarded bucket / custom domain.
		// e.g. https://files.example.com/bucket-name/prefix/ (a bucket that is
		// reverse-proxied under a directory path). The first path segment is
		// taken as the bucket (path-style); --bucket overrides this.
		t.Endpoint = u.Scheme + "://" + u.Host
		if o.Bucket != "" {
			t.Bucket = o.Bucket
		} else {
			t.Bucket, t.Key = splitFirstSegment(t.Key)
		}
		t.PathStyle = true
	}

	if t.Bucket == "" {
		return nil, fmt.Errorf("no bucket found in %q; provide it via the URL or --bucket", u.String())
	}
	return t, nil
}

// matchKnownHost recognizes the virtual-host / endpoint patterns of major
// providers and returns (provider, endpoint, bucket, region).
// bucket == "" means the service URL is path-style and the bucket must be
// taken from the first path segment.
func matchKnownHost(host string) (provider, endpoint, bucket, region string, ok bool) {
	h := strings.ToLower(host)

	if m := reAWSVHost.FindStringSubmatch(h); m != nil {
		region = m[2]
		if region == "" {
			region = "us-east-1"
		}
		return "aws", "https://s3." + region + ".amazonaws.com", m[1], region, true
	}
	if m := reAWSEndpoint.FindStringSubmatch(h); m != nil {
		region = m[1]
		if region == "" {
			region = "us-east-1"
		}
		return "aws", "https://s3." + region + ".amazonaws.com", "", region, true
	}
	if m := reAliVHost.FindStringSubmatch(h); m != nil {
		region = strings.TrimPrefix(m[2], "oss-")
		if region == "accelerate" {
			region = "cn-hangzhou"
		}
		return "aliyun", "https://" + m[2] + ".aliyuncs.com", m[1], region, true
	}
	if m := reAliEndpoint.FindStringSubmatch(h); m != nil {
		return "aliyun", "https://" + m[1] + ".aliyuncs.com", "", strings.TrimPrefix(m[1], "oss-"), true
	}
	if m := reTencentHost.FindStringSubmatch(h); m != nil {
		// m[2] looks like "cos.ap-guangzhou".
		return "tencent", "https://" + m[2] + ".myqcloud.com", m[1], strings.TrimPrefix(m[2], "cos."), true
	}
	if m := reHuaweiVHost.FindStringSubmatch(h); m != nil {
		return "huawei", "https://obs." + m[2] + ".myhuaweicloud.com", m[1], m[2], true
	}
	if m := reHuaweiEndpoint.FindStringSubmatch(h); m != nil {
		return "huawei", "https://" + h, "", m[1], true
	}
	if m := reQiniuVHost.FindStringSubmatch(h); m != nil {
		return "qiniu", "https://" + m[2] + ".qiniucs.com", m[1], strings.TrimPrefix(m[2], "s3-"), true
	}
	if m := reR2VHost.FindStringSubmatch(h); m != nil {
		return "r2", "https://" + m[2] + ".r2.cloudflarestorage.com", m[1], "auto", true
	}
	if m := reGCSVHost.FindStringSubmatch(h); m != nil {
		return "gcs", "https://storage.googleapis.com", m[1], "auto", true
	}
	if reGCSEndpoint.MatchString(h) {
		return "gcs", "https://storage.googleapis.com", "", "auto", true
	}
	// smaller / regional providers
	if m := reUCloudVHost.FindStringSubmatch(h); m != nil {
		return "ucloud", "https://" + m[2] + ".ufileos.com", m[1], strings.TrimPrefix(m[2], "s3-"), true
	}
	if m := reJDCloudEndpoint.FindStringSubmatch(h); m != nil {
		return "jdcloud", "https://" + h, "", m[1], true
	}
	if m := reKS3VHost.FindStringSubmatch(h); m != nil {
		return "ks3", "https://" + m[2] + ".ksyuncs.com", m[1], strings.TrimPrefix(m[2], "ks3-"), true
	}
	if m := reBaiduVHost.FindStringSubmatch(h); m != nil {
		return "baidu", "https://s3." + m[2] + ".bcebos.com", m[1], m[2], true
	}
	if m := reBaiduEndpoint.FindStringSubmatch(h); m != nil {
		return "baidu", "https://" + h, "", m[1], true
	}
	if m := reScalewayVHost.FindStringSubmatch(h); m != nil {
		return "scaleway", "https://s3." + m[2] + ".scw.cloud", m[1], m[2], true
	}
	if m := reScalewayEndpt.FindStringSubmatch(h); m != nil {
		return "scaleway", "https://" + h, "", m[1], true
	}
	return "", "", "", "", false
}

// consumedParams are URL query keys interpreted as S3 list-API parameters.
// Every other key is treated as an extra parameter and passed through to all
// requests (auth gateways like ?token=abc, signed URL proxies, ...).
var consumedParams = map[string]bool{
	"prefix":             true,
	"delimiter":          true,
	"max-keys":           true,
	"continuation-token": true,
	"marker":             true,
	"start-after":        true,
	"encoding-type":      true,
	"list-type":          true,
}

// applyQuery maps S3 list-API query parameters onto the target and collects
// the remaining parameters as ExtraQuery.
func applyQuery(t *Target, q url.Values) {
	if v := q.Get("prefix"); v != "" {
		t.List.Prefix = v
	}
	if _, has := q["delimiter"]; has {
		v := q.Get("delimiter")
		t.List.Delimiter = &v
	}
	if v := q.Get("max-keys"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n > 0 {
			t.List.MaxKeys = int32(n)
		}
	}
	if v := q.Get("continuation-token"); v != "" {
		t.List.ContinuationToken = v
	}
	if v := q.Get("marker"); v != "" {
		t.List.Marker = v
	}
	if v := q.Get("start-after"); v != "" {
		t.List.StartAfter = v
	}

	for k, vs := range q {
		if consumedParams[strings.ToLower(k)] {
			continue
		}
		if t.ExtraQuery == nil {
			t.ExtraQuery = make(url.Values)
		}
		t.ExtraQuery[k] = append(t.ExtraQuery[k], vs...)
	}
}

func splitFirstSegment(key string) (bucket, rest string) {
	i := strings.Index(key, "/")
	if i < 0 {
		return key, ""
	}
	return key[:i], key[i+1:]
}

func isIPLiteral(host string) bool {
	return net.ParseIP(host) != nil
}
