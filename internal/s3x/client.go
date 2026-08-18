package s3x

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Client bundles a configured S3 client with the settings it was built from.
type Client struct {
	S3        *s3.Client
	Anonymous bool
	Provider  string
	Endpoint  string
	Region    string
	PathStyle bool
}

// Resolve computes the effective endpoint / region / addressing style for the
// given options and (optional) parsed target.
//
// Precedence: explicit flags > values derived from the URL > provider
// template defaults.
func Resolve(o *ConnOpts, t *Target) (endpoint, region string, pathStyle bool, provider string, err error) {
	provider = strings.ToLower(strings.TrimSpace(o.Provider))
	if provider == "" && t != nil {
		provider = t.Provider
	}
	// Unknown-host URL targets (MinIO / Ceph / path-forwarded buckets /
	// CNAME ...): the endpoint comes from the URL itself.
	if provider == "" && t != nil && t.FromURL && t.Endpoint != "" {
		endpoint = strings.TrimRight(o.Endpoint, "/")
		if endpoint == "" {
			endpoint = t.Endpoint
		}
		region = o.Region
		if region == "" {
			region = t.Region
		}
		if region == "" {
			region = "us-east-1"
		}
		pathStyle = o.PathStyle || t.PathStyle
		if u, perr := url.Parse(endpoint); perr == nil {
			if u.Port() != "" || isIPLiteral(u.Hostname()) {
				pathStyle = true
			}
		}
		return endpoint, region, pathStyle, "custom", nil
	}
	if provider == "" {
		provider = "aws"
	}
	p, known := Providers[provider]
	if !known {
		return "", "", false, "", fmt.Errorf("unknown provider %q (choose from: %s)",
			provider, strings.Join(ProviderNames(), ", "))
	}

	region = o.Region
	if region == "" && t != nil {
		region = t.Region
	}
	if region == "" {
		region = p.DefaultRegion
	}
	if region == "" {
		region = "us-east-1"
	}

	endpoint = strings.TrimRight(o.Endpoint, "/")
	if endpoint == "" && t != nil {
		endpoint = t.Endpoint
	}
	if endpoint == "" {
		endpoint = renderEndpoint(p.EndpointTemplate, region)
	}
	if endpoint == "" {
		return "", "", false, "", fmt.Errorf("provider %q has no default endpoint; set it with -e/--endpoint", provider)
	}

	pathStyle = o.PathStyle || p.ForcePathStyle || (t != nil && t.PathStyle)
	if u, perr := url.Parse(endpoint); perr == nil {
		if u.Port() != "" || isIPLiteral(u.Hostname()) {
			pathStyle = true
		}
	}
	return endpoint, region, pathStyle, provider, nil
}

// New resolves everything and builds the S3 client.
func New(ctx context.Context, o *ConnOpts, t *Target) (*Client, error) {
	endpoint, region, pathStyle, provider, err := Resolve(o, t)
	if err != nil {
		return nil, err
	}

	creds, anonymous, err := resolveCredentials(ctx, o, region)
	if err != nil {
		return nil, err
	}

	hc := NewHTTPClient(o)
	cl := s3.NewFromConfig(aws.Config{Region: region, Credentials: creds}, func(opts *s3.Options) {
		opts.BaseEndpoint = aws.String(endpoint)
		opts.UsePathStyle = pathStyle
		opts.HTTPClient = hc
		if t != nil && len(t.ExtraQuery) > 0 {
			opts.APIOptions = append(opts.APIOptions, injectExtraQuery(t.ExtraQuery))
		}
	})
	return &Client{
		S3:        cl,
		Anonymous: anonymous,
		Provider:  provider,
		Endpoint:  endpoint,
		Region:    region,
		PathStyle: pathStyle,
	}, nil
}

// resolveCredentials picks credentials in this order:
//  1. --anonymous                      -> anonymous
//  2. --ak/--sk[/--token]              -> static
//  3. OSS_* / AWS_* environment vars   -> static
//  4. --profile or ~/.aws shared files -> AWS shared config (incl. assume-role)
//  5. otherwise                        -> anonymous
func resolveCredentials(ctx context.Context, o *ConnOpts, region string) (aws.CredentialsProvider, bool, error) {
	if o.Anonymous {
		return aws.AnonymousCredentials{}, true, nil
	}
	if o.AK != "" && o.SK != "" {
		return credentials.NewStaticCredentialsProvider(o.AK, o.SK, o.Token), false, nil
	}
	if o.AK != "" || o.SK != "" {
		return nil, false, errors.New("both --ak and --sk are required")
	}
	if ak := firstEnv("OSS_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID"); ak != "" {
		sk := firstEnv("OSS_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY")
		if sk == "" {
			return nil, false, errors.New(
				"access key id found in environment but secret key is missing (set OSS_SECRET_ACCESS_KEY or AWS_SECRET_ACCESS_KEY)")
		}
		return credentials.NewStaticCredentialsProvider(ak, sk,
			firstEnv("OSS_SESSION_TOKEN", "AWS_SESSION_TOKEN")), false, nil
	}
	if o.Profile != "" || hasSharedAWSConfig() {
		loadOpts := []func(*config.LoadOptions) error{config.WithRegion(region)}
		if o.Profile != "" {
			loadOpts = append(loadOpts, config.WithSharedConfigProfile(o.Profile))
		}
		cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
		if err == nil {
			if _, rerr := cfg.Credentials.Retrieve(ctx); rerr == nil {
				return cfg.Credentials, false, nil
			}
		}
		if o.Profile != "" {
			return nil, false, fmt.Errorf("failed to load credentials for profile %q: %w", o.Profile, err)
		}
	}
	// Nothing configured at all: fall back to anonymous access.
	return aws.AnonymousCredentials{}, true, nil
}

// ResolveCredentials exposes credential resolution for callers that issue
// their own HTTP requests instead of using the S3 client (e.g. find's
// signed probes). The bool result reports whether access is anonymous.
func ResolveCredentials(ctx context.Context, o *ConnOpts, region string) (aws.CredentialsProvider, bool, error) {
	return resolveCredentials(ctx, o, region)
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func hasSharedAWSConfig() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, f := range []string{
		filepath.Join(home, ".aws", "credentials"),
		filepath.Join(home, ".aws", "config"),
	} {
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}
	return false
}

// NewHTTPClient builds the HTTP client with proxy / TLS / extra headers.
func NewHTTPClient(o *ConnOpts) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if o.Proxy != "" {
		if pu, err := url.Parse(o.Proxy); err == nil {
			tr.Proxy = http.ProxyURL(pu)
		}
	}
	if o.Insecure {
		if tr.TLSClientConfig == nil {
			tr.TLSClientConfig = &tls.Config{}
		}
		tr.TLSClientConfig.InsecureSkipVerify = true
	}
	headers := ParseHeaders(o.Headers)
	return &http.Client{
		Transport: &headerTransport{base: tr, headers: headers},
		Timeout:   o.Timeout,
	}
}

// headerTransport injects user-supplied headers (UA, Cookie, ...) into every
// outgoing request.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if len(t.headers) > 0 {
		r2 := r.Clone(r.Context())
		for k, v := range t.headers {
			r2.Header.Set(k, v)
		}
		r = r2
	}
	return t.base.RoundTrip(r)
}

// ParseHeaders parses repeatable "Key: Value" header flags.
func ParseHeaders(raw []string) map[string]string {
	h := make(map[string]string, len(raw))
	for _, line := range raw {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		h[http.CanonicalHeaderKey(k)] = strings.TrimSpace(v)
	}
	return h
}

// injectExtraQuery appends user-supplied URL query parameters (e.g. the
// ?token=abc of an auth gateway) to every request the SDK sends. It runs in
// the Build step, before signing, so the parameters are covered by SigV4
// when credentials are in use.
func injectExtraQuery(extra url.Values) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Build.Add(middleware.BuildMiddlewareFunc("ossExtraQuery",
			func(ctx context.Context, in middleware.BuildInput, next middleware.BuildHandler) (
				middleware.BuildOutput, middleware.Metadata, error) {
				if req, ok := in.Request.(*smithyhttp.Request); ok {
					q := req.URL.Query()
					for k, vs := range extra {
						for _, v := range vs {
							q.Add(k, v)
						}
					}
					req.URL.RawQuery = q.Encode()
				}
				return next.HandleBuild(ctx, in)
			}), middleware.Before)
	}
}
