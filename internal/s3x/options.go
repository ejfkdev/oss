package s3x

import "time"

// ConnOpts carries the global connection settings parsed from CLI flags.
// They are shared by every subcommand.
type ConnOpts struct {
	// Provider selects a known endpoint template (aws, aliyun, tencent, ...).
	Provider string
	// Endpoint overrides the provider endpoint entirely.
	Endpoint string
	// Region overrides the region used for signing / endpoint rendering.
	Region string
	// Bucket explicitly names the bucket when the URL host does not reveal it.
	Bucket string
	// PathStyle forces path-style addressing (http://host/bucket/key).
	PathStyle bool

	// AK / SK / Token are static credentials (Token = STS session token).
	AK    string
	SK    string
	Token string
	// Profile loads credentials from the AWS shared config (~/.aws/config),
	// which also supports assume-role (STS) profiles.
	Profile string
	// Anonymous forces anonymous (credential-less) access.
	Anonymous bool

	// Proxy is an HTTP(S) proxy URL (curl-style -x).
	Proxy string
	// Headers are raw "Key: Value" pairs injected into every request
	// (User-Agent, Cookie, ...).
	Headers []string
	// Insecure skips TLS certificate verification.
	Insecure bool
	// Timeout bounds a single HTTP request; 0 disables the timeout.
	Timeout time.Duration
}
