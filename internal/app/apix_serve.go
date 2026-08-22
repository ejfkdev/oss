package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	errs "github.com/ejfkdev/xyz-go/errors"
	"github.com/ejfkdev/xyz-go/httpapi"
	"github.com/ejfkdev/xyz-go/mcp"
	"github.com/ejfkdev/xyz-go/registry"
	"github.com/ejfkdev/xyz-go/spec"
)

// serveArgs starts the HTTP service exposing the xyz-go registry: REST routes
// (ls/stat/cat/presign/find), /openapi.json, /healthz and MCP streamable HTTP
// under /mcp. CLI-only (long-running; no HTTP/MCP hints).
type serveArgs struct {
	Addr    string        `json:"addr,omitempty" desc:"监听地址 / listen address" default:":8080"`
	Bearer  []string      `json:"bearer,omitempty" desc:"Bearer 令牌（可重复）/ bearer token(s), repeatable"`
	CORS    []string      `json:"cors,omitempty" desc:"CORS 允许来源（可重复；* 允许任意）/ CORS origins"`
	Timeout time.Duration `json:"timeout,omitempty" desc:"读写/空闲超时 / read/write/idle timeout"`
	TLSCert string        `json:"tls-cert,omitempty" desc:"TLS 证书文件 / TLS cert file"`
	TLSKey  string        `json:"tls-key,omitempty" desc:"TLS 私钥文件 / TLS key file"`
	Color   string        `json:"color,omitempty" desc:"彩色输出 auto|always|never（仅 CLI）" default:"auto"`
}

func registerCliServe(reg *registry.Registry) error {
	_, err := spec.Define("serve", apixServe).
		Summary(T("启动 HTTP 服务：REST + OpenAPI + /mcp", "start the HTTP service: REST + OpenAPI + /mcp")).
		Description(T("一个端口提供 REST 路由、/openapi.json、/healthz 与 /mcp（MCP streamable HTTP）。默认不鉴权。",
			"One port serves the REST routes, /openapi.json, /healthz and /mcp (MCP streamable HTTP). No auth by default.")).
		CLI(spec.CliHints{
			Usage:  "serve [--addr :8080]",
			Daemon: true,
			After: T(`示例:
   oss serve --addr 127.0.0.1:8080
   curl -s '127.0.0.1:8080/ls?target=https://files.example.com/bucket/&prefix=logs/'
   curl -s '127.0.0.1:8080/cat?target=s3://mybucket/app.log&range=0-1023'
   oss serve --addr :8443 --tls-cert cert.pem --tls-key key.pem --bearer s3cret

说明:
   - 默认不鉴权——请勿直接暴露公网；--bearer 开启 Bearer 令牌校验
   - 凭证每次请求通过 header 传入（X-Oss-Ak/X-Oss-Sk/X-Oss-Token/X-Oss-Profile），
     未传时回落服务端环境变量（OSS_* / AWS_*）与 ~/.aws 共享配置`,
				`EXAMPLES:
   oss serve --addr 127.0.0.1:8080
   curl -s '127.0.0.1:8080/ls?target=https://files.example.com/bucket/&prefix=logs/'
   curl -s '127.0.0.1:8080/cat?target=s3://mybucket/app.log&range=0-1023'
   oss serve --addr :8443 --tls-cert cert.pem --tls-key key.pem --bearer s3cret

NOTES:
   - No auth by default — do not expose it to the internet; --bearer enables
     Bearer-token verification
   - Credentials are passed per request via headers (X-Oss-Ak/X-Oss-Sk/X-Oss-Token/X-Oss-Profile),
     falling back to the server environment (OSS_* / AWS_*) and ~/.aws`),
		}).
		Register(reg)
	return err
}

func apixServe(ctx context.Context, in *serveArgs) (int, error) {
	bearers := in.Bearer
	origins := in.CORS
	handler, mcpHandler, err := serveHandlers(bearers, origins)
	if err != nil {
		return 2, err
	}
	outer := http.NewServeMux()
	outer.Handle("GET /cat", rawCatHandler())
	outer.Handle("/mcp", mcpHandler)
	outer.Handle("/", handler)
	// Middleware chain (outermost first): CORS preflight (before auth, browser
	// preflights carry no credentials) -> Bearer -> Gzip -> routes.
	root := httpapi.CORS(origins, httpapi.Bearer(bearers, httpapi.Gzip(outer)))

	if (in.TLSCert == "") != (in.TLSKey == "") {
		return 2, errors.New(T("TLS 需要同时给定 --tls-cert 与 --tls-key", "TLS requires both --tls-cert and --tls-key"))
	}
	scheme := "http"
	if in.TLSCert != "" {
		scheme = "https"
	}
	addr := in.Addr
	if addr == "" {
		addr = ":8080"
	}

	srv := &http.Server{Addr: addr, Handler: root, ReadHeaderTimeout: 10 * time.Second}
	srv.BaseContext = func(net.Listener) context.Context { return ctx }
	if in.Timeout > 0 {
		srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout = in.Timeout, in.Timeout, in.Timeout
	}

	fmt.Fprintf(os.Stdout, "%s %s://%s（REST + /openapi.json + /mcp）\n",
		cGreen(T("服务已启动", "listening")), scheme, addr)
	if len(bearers) == 0 {
		fmt.Fprintln(os.Stderr, eYellow(T(
			"⚠ 未设置 --bearer，服务不鉴权；如对外暴露请务必配置令牌",
			"⚠ no --bearer set, the service is unauthenticated; set tokens before exposing it")))
	}

	errc := make(chan error, 1)
	go func() {
		if in.TLSCert != "" {
			errc <- srv.ListenAndServeTLS(in.TLSCert, in.TLSKey)
		} else {
			errc <- srv.ListenAndServe()
		}
	}()
	select {
	case serveErr := <-errc:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return 1, serveErr
		}
		return 0, nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		fmt.Fprintln(os.Stderr, cGreen(T("已优雅关停", "shut down gracefully")))
		return 0, nil
	}
}

// serveHandlers builds the REST handler and the MCP streamable-HTTP handler
// sharing the API registry (exported for tests).
func serveHandlers(bearers, origins []string) (http.Handler, http.Handler, error) {
	reg, err := BuildAPIRegistry()
	if err != nil {
		return nil, nil, err
	}
	handler, err := httpapi.Handler(reg)
	if err != nil {
		return nil, nil, err
	}
	mcpHandler, err := mcp.HTTPHandler(reg, mcp.Options{
		BearerTokens: bearers, CORSOrigins: origins, Instructions: mcpInstructions,
	})
	if err != nil {
		return nil, nil, err
	}
	return handler, mcpHandler, nil
}

// rawCatHandler streams an object body verbatim — the raw byte stream the
// JSON-only registry routes can't express. Range can come from the `range`
// query param (same syntax as the CLI --range) or a standard Range header;
// the S3 response's Content-Type/Content-Length/Content-Range (206 on range
// reads) are passed through.
func rawCatHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		target := q.Get("target")
		rng := q.Get("range")
		if rng == "" {
			rng = r.Header.Get("Range")
		}
		if target == "" {
			writeAPIError(w, false, errs.New(errs.KindInvalidInput,
				T("缺少 target 参数：/cat?target=<桶/对象>", "missing target parameter: /cat?target=<bucket/object>")))
			return
		}
		o := connOptsFromRequest(r)
		_, resp, err := catTarget(r.Context(), o, target, rng)
		if err != nil {
			writeAPIError(w, o.Anonymous, err)
			return
		}
		defer resp.Body.Close()

		ct := aws.ToString(resp.ContentType)
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		if resp.ContentLength != nil && *resp.ContentLength >= 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(*resp.ContentLength, 10))
		}
		status := http.StatusOK
		if rng != "" || aws.ToString(resp.ContentRange) != "" {
			status = http.StatusPartialContent
			if cr := aws.ToString(resp.ContentRange); cr != "" {
				w.Header().Set("Content-Range", cr)
			}
		}
		w.WriteHeader(status)
		_, _ = io.Copy(w, resp.Body)
	}
}
