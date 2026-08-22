package app

// The xyz-go-backed API surface (HTTP REST + MCP) for oss. The CLI keeps its
// urfave frontend; these definitions back `oss serve` and `oss mcp`.
//
// Command names are flat (ls / stat / presign / find). Every args struct
// carries the 14 connection fields inline — xyz-go rejects nested or embedded
// arg structs, so they must be spelled out per command. The canonical field
// set (tags must stay identical on every command):
//
//	AK          string  `json:"ak,omitempty" desc:"访问密钥 ID / access key id" secret:"true" http:"header" httpName:"X-Oss-Ak"`
//	SK          string  `json:"sk,omitempty" desc:"访问密钥 / secret key" secret:"true" http:"header" httpName:"X-Oss-Sk"`
//	Token       string  `json:"token,omitempty" desc:"STS 会话令牌 / session token" secret:"true" http:"header" httpName:"X-Oss-Token"`
//	Profile     string  `json:"profile,omitempty" desc:"AWS 共享配置 profile / shared config profile" http:"header" httpName:"X-Oss-Profile"`
//	Anonymous   bool    `json:"anonymous,omitempty" desc:"强制匿名访问 / force anonymous"`
//	Provider    string  `json:"provider,omitempty" desc:"云厂商 / provider"`
//	Endpoint    string  `json:"endpoint,omitempty" desc:"自定义 S3 端点 / custom endpoint"`
//	Region      string  `json:"region,omitempty" desc:"地域 / region"`
//	PathStyle   bool    `json:"path_style,omitempty" desc:"路径风格寻址 / path-style addressing"`
//	Bucket      string  `json:"bucket,omitempty" desc:"桶名（配合 provider 使用）/ bucket name"`
//	Proxy       string  `json:"proxy,omitempty" desc:"HTTP(S) 代理 / proxy"`
//	Headers     []string `json:"headers,omitempty" desc:"附加请求头，Key: Value / extra headers"`
//	Insecure    bool    `json:"insecure,omitempty" desc:"跳过 TLS 校验 / skip TLS verification"`
//	Timeout     time.Duration `json:"timeout,omitempty" desc:"请求超时，如 15s / request timeout"`

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"encoding/json"
	errs "github.com/ejfkdev/xyz-go/errors"
	"github.com/ejfkdev/xyz-go/registry"
	"github.com/ejfkdev/xyz-go/spec"

	"github.com/ejfkdev/oss/internal/s3x"
)

// xyzHintsGET is the HTTP hint shared by all API commands: a read-only GET
// route with every field bound from the query string (credentials excepted —
// those bind from headers per the canonical field set).
func xyzHintsGET(path string) spec.HTTPHints {
	return spec.HTTPHints{Method: "GET", Path: path}
}

// xyzMCPRead marks a tool as read-only for MCP clients.
func xyzMCPRead() spec.MCPHints {
	return spec.MCPHints{Annotations: []string{"read"}}
}

// apixConnShortcuts merges the standard connection shorthands (-e/-x/-H/-k,
// matching the pre-migration CLI) into a per-command CliFieldHint map.
func apixConnShortcuts(extra map[string]spec.CliFieldHint) map[string]spec.CliFieldHint {
	m := map[string]spec.CliFieldHint{
		"endpoint": {Shorthand: "e"},
		"proxy":    {Shorthand: "x"},
		"headers":  {Shorthand: "H"},
		"insecure": {Shorthand: "k"},
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// mcpInstructions is the usage note shown to MCP clients right after
// initialization (the "help" of the MCP surface).
var mcpInstructions = T(`5 个工具：ls 列举、stat 元数据、presign 预签名、find 桶归属探测、cat 读内容。
目标写 s3://桶/键 或完整 URL；凭证作为工具参数 ak/sk/token（STS 加 token）传入，
未提供时用服务端环境变量（OSS_* / AWS_*）或 ~/.aws。cat 返回 UTF-8 文本
(text 字段)或二进制 base64（base64 字段），单次上限 16MiB。`,
	`Five tools: ls (list), stat (metadata), presign (pre-signed URL),
find (bucket discovery), cat (read content). Targets are s3://bucket/key or
full URLs; credentials go in tool arguments ak/sk (plus token for STS),
falling back to the server environment (OSS_* / AWS_*) or ~/.aws. cat returns
UTF-8 text (text field) or base64 (base64 field), capped at 16MiB per call.`)

// connOptsFrom folds the inline connection fields of an API args struct into
// the shared connection options.
func connOptsFrom(ak, sk, token, profile, provider, endpoint, region, proxy, bucket string,
	anonymous, pathStyle, insecure bool, headers []string, timeout time.Duration) *s3x.ConnOpts {
	return &s3x.ConnOpts{
		AK: ak, SK: sk, Token: token, Profile: profile,
		Provider: provider, Endpoint: endpoint, Region: region,
		Bucket: bucket, PathStyle: pathStyle, Anonymous: anonymous,
		Proxy: proxy, Headers: headers, Insecure: insecure, Timeout: timeout,
	}
}

// BuildAPIRegistry assembles the xyz command registry backing the HTTP and
// MCP interfaces.
func BuildAPIRegistry() (*registry.Registry, error) {
	reg := registry.New()
	for _, fn := range []func(*registry.Registry) error{
		registerApixLs, registerApixStat, registerApixPresign, registerApixFind, registerApixCat,
	} {
		if err := fn(reg); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

// writeAPIError renders an API error as the standard JSON body with a mapped
// HTTP status code — the same shape the xyz HTTP frontend produces.
func writeAPIError(w http.ResponseWriter, anonymous bool, err error) {
	status := errs.HTTPStatus(apixClassify(err))
	msg := apixErr(anonymous, err).Error()
	line, _ := json.Marshal(map[string]any{"error": msg})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(line)
	_, _ = w.Write([]byte("\n"))
}

// connOptsFromRequest builds connection options for the raw (non-registry)
// routes: credentials come from the X-Oss-* headers (never the URL), the other
// connection parameters from same-named query params — the conventions
// documented for the registry routes.
func connOptsFromRequest(r *http.Request) *s3x.ConnOpts {
	q := r.URL.Query()
	boolQ := func(name string) bool {
		v, _ := strconv.ParseBool(q.Get(name))
		return v
	}
	d, _ := time.ParseDuration(q.Get("timeout"))
	return &s3x.ConnOpts{
		AK: r.Header.Get("X-Oss-Ak"), SK: r.Header.Get("X-Oss-Sk"),
		Token: r.Header.Get("X-Oss-Token"), Profile: r.Header.Get("X-Oss-Profile"),
		Provider: q.Get("provider"), Endpoint: q.Get("endpoint"), Region: q.Get("region"),
		PathStyle: boolQ("path_style"), Bucket: q.Get("bucket"), Proxy: q.Get("proxy"),
		Headers: q["headers"], Insecure: boolQ("insecure"), Timeout: d,
		Anonymous: boolQ("anonymous"),
	}
}

// apixErr annotates an S3-layer error for the HTTP/MCP interfaces: the human
// friendly apiErr text as the message, the API taxonomy as the kind. Errors
// that already carry an API kind pass through untouched.
func apixErr(anonymous bool, err error) error {
	if err == nil {
		return nil
	}
	if k := errs.Classify(err); k != errs.KindInternal && k != errs.KindNone {
		return err
	}
	msg := err.Error()
	if annotated := apiErr(err, anonymous); annotated != nil {
		msg = annotated.Error()
	}
	return errs.WrapMsg(apixClassify(err), err, msg)
}

// apixClassify maps an error from the S3 layer onto the API taxonomy. It
// honors xyz-go's own coded errors first, then inspects the smithy error
// code; transport-level conditions are recognized from the message.
func apixClassify(err error) errs.Kind {
	if err == nil {
		return errs.KindInternal
	}
	if k := errs.Classify(err); k != errs.KindInternal && k != errs.KindNone {
		return k
	}
	if errors.Is(err, context.Canceled) {
		return errs.KindCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errs.KindUnavailable
	}
	var ae interface{ ErrorCode() string }
	if errors.As(err, &ae) {
		switch code := ae.ErrorCode(); {
		case isCredentialCode(code):
			return errs.KindUnauthorized
		case code == "AccessDenied" || code == "AllAccessDisabled":
			return errs.KindForbidden
		case code == "NoSuchBucket" || code == "NoSuchKey" || code == "NoSuchObject" ||
			code == "NotFound" || code == "NoSuchUpload":
			return errs.KindNotFound
		case code == "MethodNotAllowed" || code == "MalformedXML" ||
			code == "InvalidRequest" || code == "MissingSecurityHeader":
			return errs.KindInvalidInput
		case code != "":
			return errs.KindInternal
		}
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"),
		strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"),
		strings.Contains(msg, "tls handshake"), strings.Contains(msg, "interrupted"):
		return errs.KindUnavailable
	case strings.Contains(msg, "无法解析"), strings.Contains(msg, "unparseable"),
		strings.Contains(msg, "usage: "), strings.Contains(msg, "用法: "):
		return errs.KindInvalidInput
	}
	return errs.KindInternal
}

// isCredentialCode reports whether an S3 error code means the credentials
// themselves were rejected.
func isCredentialCode(code string) bool {
	switch code {
	case "InvalidAccessKeyId", "SignatureDoesNotMatch", "ExpiredToken", "TokenRefreshRequired",
		"InvalidToken", "InvalidSecurity", "AuthFailure", "AuthorizationHeaderMalformed",
		"UnrecognizedClientException", "RequestTimeTooSkewed", "MissingAuthenticationToken",
		"InvalidAccessKey":
		return true
	}
	return false
}
