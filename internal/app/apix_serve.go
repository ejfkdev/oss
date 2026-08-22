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
	"github.com/urfave/cli/v3"
)

// serveCmd starts the HTTP service exposing the xyz-go registry: REST routes
// (ls/stat/presign/find), /openapi.json, /healthz and MCP streamable HTTP
// under /mcp — the same model as xyz-go's own serve mode.
func serveCmd() *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: T("启动 HTTP 服务：REST API + OpenAPI + MCP", "start the HTTP service: REST API + OpenAPI + MCP"),
		UsageText: T(`oss serve [--addr :8080]

在同一端口提供:
   REST 路由      GET /ls /stat /presign /find（凭证走 X-Oss-Ak/X-Oss-Sk/X-Oss-Token 请求头）
   RAW 字节流     GET /cat（对象内容原样输出，支持 range 范围读取）
   OpenAPI 文档   GET /openapi.json
   健康检查       GET /healthz
   MCP            /mcp（MCP streamable HTTP 工具端点，工具含 cat）

示例:
   oss serve --addr 127.0.0.1:8080
   curl -s '127.0.0.1:8080/ls?target=https://files.example.com/bucket/&prefix=logs/'
   curl -s '127.0.0.1:8080/cat?target=s3://mybucket/app.log&range=0-1023'
   oss serve --addr :8443 --tls-cert cert.pem --tls-key key.pem --bearer s3cret

说明:
   - 默认不鉴权——请勿直接暴露公网；--bearer 开启 Bearer 令牌校验
   - 凭证每次请求通过 header 传入（X-Oss-Ak/X-Oss-Sk/X-Oss-Token/X-Oss-Profile），
     未传时回落服务端环境变量（OSS_* / AWS_*）与 ~/.aws 共享配置`,
			`oss serve [--addr :8080]

Serves on one port:
   REST routes    GET /ls /stat /presign /find (credentials via X-Oss-Ak/X-Oss-Sk/X-Oss-Token headers)
   RAW byte stream GET /cat (verbatim object content, range reads supported)
   OpenAPI doc    GET /openapi.json
   health probe   GET /healthz
   MCP            /mcp (MCP streamable HTTP tool endpoint; tools include cat)

EXAMPLES:
   oss serve --addr 127.0.0.1:8080
   curl -s '127.0.0.1:8080/ls?target=https://files.example.com/bucket/&prefix=logs/'
   curl -s '127.0.0.1:8080/cat?target=s3://mybucket/app.log&range=0-1023'
   oss serve --addr :8443 --tls-cert cert.pem --tls-key key.pem --bearer s3cret

NOTES:
   - No auth by default — do not expose it to the internet; --bearer enables
     Bearer-token verification
   - Credentials are passed per request via headers (X-Oss-Ak/X-Oss-Sk/X-Oss-Token),
     or provided globally by the server process environment (OSS_* / AWS_*)`),
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "addr", Value: ":8080", Usage: T("监听地址", "listen address")},
			&cli.StringSliceFlag{Name: "bearer", Usage: T("Bearer 令牌（逗号分隔，可多次；空=不鉴权）", "bearer token(s), comma-separated; empty = no auth")},
			&cli.StringSliceFlag{Name: "cors", Usage: T("CORS 允许来源（逗号分隔；* 允许任意）", "CORS allowed origins; * = any")},
			&cli.DurationFlag{Name: "timeout", Usage: T("读写/空闲超时（0=不限，仍保留 10s 请求头超时）", "read/write/idle timeout (0 = none, keeps the 10s header timeout)")},
			&cli.StringFlag{Name: "tls-cert", Usage: T("TLS 证书文件（与 --tls-key 同时给出启用 HTTPS）", "TLS cert file")},
			&cli.StringFlag{Name: "tls-key", Usage: T("TLS 私钥文件", "TLS key file")},
		},
		Action: runServe,
	}
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

func runServe(ctx context.Context, c *cli.Command) error {
	reg, err := BuildAPIRegistry()
	if err != nil {
		return err
	}
	handler, err := httpapi.Handler(reg)
	if err != nil {
		return err
	}
	bearers := c.StringSlice("bearer")
	origins := c.StringSlice("cors")
	mcpHandler, err := mcp.HTTPHandler(reg, mcp.Options{
		BearerTokens: bearers, CORSOrigins: origins, Instructions: mcpInstructions,
	})
	if err != nil {
		return err
	}
	outer := http.NewServeMux()
	outer.Handle("GET /cat", rawCatHandler())
	outer.Handle("/mcp", mcpHandler)
	outer.Handle("/", handler)
	// Middleware chain (outermost first): CORS preflight (before auth, browser
	// preflights carry no credentials) -> Bearer -> Gzip -> routes.
	root := httpapi.CORS(origins, httpapi.Bearer(bearers, httpapi.Gzip(outer)))

	tlsCert, tlsKey := c.String("tls-cert"), c.String("tls-key")
	if (tlsCert == "") != (tlsKey == "") {
		return errors.New(T("TLS 需要同时给定 --tls-cert 与 --tls-key", "TLS requires both --tls-cert and --tls-key"))
	}
	scheme := "http"
	if tlsCert != "" {
		scheme = "https"
	}

	srv := &http.Server{Addr: c.String("addr"), Handler: root, ReadHeaderTimeout: 10 * time.Second}
	srv.BaseContext = func(net.Listener) context.Context { return ctx }
	if d := c.Duration("timeout"); d > 0 {
		srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout = d, d, d
	}

	fmt.Fprintf(os.Stdout, "%s %s://%s（REST + /openapi.json + /mcp）\n",
		cGreen(T("服务已启动", "listening")), scheme, c.String("addr"))
	if len(bearers) == 0 {
		fmt.Fprintln(os.Stderr, eYellow(T(
			"⚠ 未设置 --bearer，服务不鉴权；如对外暴露请务必配置令牌",
			"⚠ no --bearer set, the service is unauthenticated; set tokens before exposing it")))
	}

	errc := make(chan error, 1)
	go func() {
		if tlsCert != "" {
			errc <- srv.ListenAndServeTLS(tlsCert, tlsKey)
		} else {
			errc <- srv.ListenAndServe()
		}
	}()
	select {
	case serveErr := <-errc:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		fmt.Fprintln(os.Stderr, cGreen(T("已优雅关停", "shut down gracefully")))
		return nil
	}
}
