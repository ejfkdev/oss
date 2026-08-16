package s3x

import (
	"sort"
	"strings"
)

// Provider describes an S3-compatible storage service.
type Provider struct {
	Name string
	// EndpointTemplate renders the service endpoint; "{region}" is replaced.
	// Empty means the user must supply --endpoint (e.g. minio, r2).
	EndpointTemplate string
	DefaultRegion    string
	ForcePathStyle   bool
}

// Providers is the registry of well-known S3-compatible services.
var Providers = map[string]Provider{
	"aws": {
		Name: "aws", EndpointTemplate: "https://s3.{region}.amazonaws.com", DefaultRegion: "us-east-1",
	},
	"aliyun": {
		Name: "aliyun", EndpointTemplate: "https://s3.{region}.oss.aliyuncs.com", DefaultRegion: "cn-hangzhou",
	},
	"tencent": {
		Name: "tencent", EndpointTemplate: "https://cos.{region}.myqcloud.com", DefaultRegion: "ap-guangzhou",
	},
	"huawei": {
		Name: "huawei", EndpointTemplate: "https://obs.{region}.myhuaweicloud.com", DefaultRegion: "cn-north-4",
	},
	"qiniu": {
		Name: "qiniu", EndpointTemplate: "https://s3.{region}.qiniucs.com", DefaultRegion: "cn-east-1",
	},
	"baidu": {
		Name: "baidu", EndpointTemplate: "https://s3.{region}.bcebos.com", DefaultRegion: "bj",
	},
	"ks3": {
		Name: "ks3", EndpointTemplate: "https://ks3-{region}.ksyuncs.com", DefaultRegion: "cn-beijing",
	},
	"ucloud": {
		Name: "ucloud", EndpointTemplate: "https://s3-{region}.ufileos.com", DefaultRegion: "cn-sh2",
	},
	"jdcloud": {
		Name: "jdcloud", EndpointTemplate: "https://s3.{region}.jdcloud-oss.com", DefaultRegion: "cn-north-1",
		ForcePathStyle: true, // JD Cloud docs recommend path-style access
	},
	"scaleway": {
		Name: "scaleway", EndpointTemplate: "https://s3.{region}.scw.cloud", DefaultRegion: "fr-par",
	},
	"gcs": {
		Name: "gcs", EndpointTemplate: "https://storage.googleapis.com", DefaultRegion: "auto", ForcePathStyle: true,
	},
	"r2": {
		Name: "r2", DefaultRegion: "auto", // requires -e https://<account_id>.r2.cloudflarestorage.com
	},
	"wasabi": {
		Name: "wasabi", EndpointTemplate: "https://s3.{region}.wasabisys.com", DefaultRegion: "us-east-1",
	},
	"spaces": {
		Name: "spaces", EndpointTemplate: "https://{region}.digitaloceanspaces.com", DefaultRegion: "nyc3",
	},
	"b2": {
		Name: "b2", EndpointTemplate: "https://s3.{region}.backblazeb2.com", DefaultRegion: "us-west-004",
	},
	"minio": {
		Name: "minio", ForcePathStyle: true, // requires -e http://host:9000
	},
}

// ProviderNames returns the sorted list of registered provider names.
func ProviderNames() []string {
	names := make([]string, 0, len(Providers))
	for n := range Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// providerDisplay maps a provider key to a human-friendly display name.
var providerDisplay = map[string]string{
	"aws": "AWS S3", "aliyun": "Aliyun OSS", "tencent": "Tencent COS",
	"huawei": "Huawei OBS", "qiniu": "Qiniu Kodo", "baidu": "Baidu BOS",
	"ks3": "Kingsoft KS3", "ucloud": "UCloud US3", "jdcloud": "JD Cloud OSS",
	"scaleway": "Scaleway Object Storage", "gcs": "Google Cloud Storage",
	"r2": "Cloudflare R2", "wasabi": "Wasabi", "spaces": "DigitalOcean Spaces",
	"b2": "Backblaze B2", "minio": "MinIO", "custom": "Custom",
}

// ProviderDisplayName returns a display name for a provider key.
func ProviderDisplayName(key string) string {
	if n, ok := providerDisplay[key]; ok {
		return n
	}
	if key == "" {
		return providerDisplay["custom"]
	}
	return key
}

func renderEndpoint(template, region string) string {
	if template == "" {
		return ""
	}
	return strings.ReplaceAll(template, "{region}", region)
}

// ScanProbe describes how to probe bucket existence on one provider for the
// `oss scan` command. The probe sends an anonymous GET to the constructed
// URL and interprets the HTTP status: 200/3xx/401/403 means the bucket
// exists (public or private), 404 means it does not, anything else is
// reported as unknown.
type ScanProbe struct {
	Provider string // provider key (matches the Providers map)
	Name     string // display name
	// URLTemplate builds the probe URL; {bucket} and {region} are substituted.
	URLTemplate string
	// Regions lists the regions probed for regional services. Empty means a
	// single probe and URLTemplate must not contain {region}.
	Regions []string
}

// ScanProbes covers every provider with a predictable bucket URL. Not
// included: R2 (endpoint embeds an account ID), MinIO/custom (self-hosted),
// Backblaze B2 (returns 403 for every bucket, so anonymous probes cannot
// distinguish existing from non-existing), and Qiniu Kodo (rejects all
// anonymous requests with a signature error).
var ScanProbes = []ScanProbe{
	{Provider: "aws", Name: "AWS S3",
		URLTemplate: "https://{bucket}.s3.amazonaws.com"}, // global endpoint, any region
	{Provider: "aliyun", Name: "Aliyun OSS",
		URLTemplate: "https://{bucket}.oss-{region}.aliyuncs.com",
		Regions:     []string{"cn-hangzhou", "cn-beijing", "cn-shanghai"}},
	{Provider: "tencent", Name: "Tencent COS",
		URLTemplate: "https://{bucket}.cos.{region}.myqcloud.com",
		Regions:     []string{"ap-guangzhou", "ap-shanghai", "ap-beijing"}},
	{Provider: "huawei", Name: "Huawei OBS",
		URLTemplate: "https://{bucket}.obs.{region}.myhuaweicloud.com",
		Regions:     []string{"cn-north-4", "cn-east-3"}},
	{Provider: "baidu", Name: "Baidu BOS",
		URLTemplate: "https://{bucket}.s3.{region}.bcebos.com",
		Regions:     []string{"bj", "gz", "su"}},
	{Provider: "ks3", Name: "Kingsoft KS3",
		URLTemplate: "https://{bucket}.ks3-{region}.ksyuncs.com",
		Regions:     []string{"cn-beijing", "cn-shanghai"}},
	{Provider: "ucloud", Name: "UCloud US3",
		URLTemplate: "https://{bucket}.s3-{region}.ufileos.com",
		Regions:     []string{"cn-sh2", "cn-bj"}},
	{Provider: "jdcloud", Name: "JD Cloud OSS",
		URLTemplate: "https://s3.{region}.jdcloud-oss.com/{bucket}",
		Regions:     []string{"cn-north-1"}},
	{Provider: "scaleway", Name: "Scaleway Object Storage",
		URLTemplate: "https://{bucket}.s3.{region}.scw.cloud",
		Regions:     []string{"fr-par"}},
	{Provider: "gcs", Name: "Google Cloud Storage",
		URLTemplate: "https://storage.googleapis.com/{bucket}"},
	{Provider: "wasabi", Name: "Wasabi",
		URLTemplate: "https://s3.{region}.wasabisys.com/{bucket}",
		Regions:     []string{"us-east-1"}},
	{Provider: "spaces", Name: "DigitalOcean Spaces",
		URLTemplate: "https://{region}.digitaloceanspaces.com/{bucket}",
		Regions:     []string{"nyc3"}},
}

// ScanURLs renders all probe URLs for one probe. If regionOverride is
// non-empty and the probe is regional, only that region is probed.
func (p ScanProbe) ScanURLs(bucket, regionOverride string) []string {
	regions := p.Regions
	if regionOverride != "" && len(p.Regions) > 0 {
		regions = []string{regionOverride}
	}
	if len(regions) == 0 {
		return []string{strings.ReplaceAll(p.URLTemplate, "{bucket}", bucket)}
	}
	urls := make([]string, 0, len(regions))
	for _, r := range regions {
		u := strings.ReplaceAll(p.URLTemplate, "{bucket}", bucket)
		u = strings.ReplaceAll(u, "{region}", r)
		urls = append(urls, u)
	}
	return urls
}
