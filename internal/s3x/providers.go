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

func renderEndpoint(template, region string) string {
	if template == "" {
		return ""
	}
	return strings.ReplaceAll(template, "{region}", region)
}
