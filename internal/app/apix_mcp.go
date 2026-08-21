package app

import (
	"context"
	"errors"
	"strings"

	"github.com/ejfkdev/xyz-go/mcp"
	"github.com/urfave/cli/v3"
)

// mcpCmd starts the MCP tool server (stdio / SSE / streamable HTTP) backed by
// the same registry that powers `oss serve`. It forwards the transport word
// and flags to xyz-go's MCP frontend.
func mcpCmd() *cli.Command {
	return &cli.Command{
		Name:  "mcp",
		Usage: T("启动 MCP 工具服务器（stdio/sse/http）", "start the MCP tool server (stdio/sse/http)"),
		UsageText: T(`oss mcp <stdio|http|sse> [选项]

把 ls / stat / presign / find 暴露为 MCP 工具（名称即命令名），
供支持 MCP 的客户端（如 Claude 等）调用。

示例:
   oss mcp stdio                             本地进程标准输入/输出（最常用）
   oss mcp http --addr :9000 --bearer tok    远程 streamable HTTP
   oss mcp sse --addr 127.0.0.1:9000         SSE 传输（局域网内可用）

选项:
   --addr <ADDR>            监听地址（http/sse，默认 :8080）
   --versions <LIST>        限定协议版本（逗号分隔）
   --bearer <TOK>           Bearer 令牌（可多次；http/sse 生效，stdio 不受影响）
   --cors <LIST>            CORS 允许来源
   --name <NAME>            服务器名称（默认 oss）
   --server-version <VER>   服务器版本
   --stateless              流式 HTTP 无状态模式
   --json-response          流式 HTTP 以 application/json 应答
   --session-timeout <DUR>  会话空闲超时（如 30m）`,
			`oss mcp <stdio|http|sse> [options]

Exposes ls / stat / presign / find as MCP tools (tool names = command names)
for MCP-capable clients (Claude etc.).

EXAMPLES:
   oss mcp stdio                             local process over stdio (most common)
   oss mcp http --addr :9000 --bearer tok    remote streamable HTTP
   oss mcp sse --addr 127.0.0.1:9000         SSE transport

OPTIONS:
   --addr <ADDR>            listen address (http/sse, default :8080)
   --versions <LIST>        pin protocol versions (comma-separated)
   --bearer <TOK>           bearer token(s) (repeatable; http/sse only, stdio unaffected)
   --cors <LIST>            CORS allowed origins
   --name <NAME>            server name (default oss)
   --server-version <VER>   server version
   --stateless              streamable HTTP stateless mode
   --json-response          streamable HTTP answers application/json
   --session-timeout <DUR>  idle session expiry (e.g. 30m)`),
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "addr", Usage: T("监听地址（http/sse）", "listen address (http/sse)")},
			&cli.StringFlag{Name: "versions", Usage: T("限定协议版本（逗号分隔）", "pin protocol versions (comma-separated)")},
			&cli.StringSliceFlag{Name: "bearer", Usage: T("Bearer 令牌（逗号分隔，可多次）", "bearer token(s), comma-separated")},
			&cli.StringSliceFlag{Name: "cors", Usage: T("CORS 允许来源", "CORS allowed origins")},
			&cli.StringFlag{Name: "name", Usage: T("服务器名称", "server name")},
			&cli.StringFlag{Name: "server-version", Usage: T("服务器版本", "server version")},
			&cli.BoolFlag{Name: "stateless", Usage: T("流式 HTTP 无状态模式", "streamable HTTP stateless mode")},
			&cli.BoolFlag{Name: "json-response", Usage: T("流式 HTTP 以 JSON 应答", "streamable HTTP answers JSON")},
			&cli.DurationFlag{Name: "session-timeout", Usage: T("会话空闲超时", "idle session expiry")},
		},
		Action: runMCP,
	}
}

func runMCP(ctx context.Context, c *cli.Command) error {
	transport := strings.ToLower(c.Args().First())
	switch transport {
	case "stdio", "http", "sse":
	default:
		return errors.New(T("用法: oss mcp <stdio|http|sse>", "usage: oss mcp <stdio|http|sse>"))
	}

	reg, err := BuildAPIRegistry()
	if err != nil {
		return err
	}

	// Forward the transport word plus the explicitly set flags in xyz-go's
	// own flag syntax.
	args := []string{transport}
	add := func(name, value string) {
		if value != "" {
			args = append(args, "--"+name, value)
		}
	}
	add("addr", c.String("addr"))
	add("versions", c.String("versions"))
	if v := strings.Join(c.StringSlice("bearer"), ","); v != "" {
		add("bearer", v)
	}
	if v := strings.Join(c.StringSlice("cors"), ","); v != "" {
		add("cors", v)
	}
	add("name", c.String("name"))
	add("server-version", c.String("server-version"))
	if d := c.Duration("session-timeout"); d > 0 {
		add("session-timeout", d.String())
	}
	if c.Bool("stateless") {
		args = append(args, "--stateless")
	}
	if c.Bool("json-response") {
		args = append(args, "--json-response")
	}

	code := mcp.RunContextWithOptions(ctx, reg, args, mcp.Options{Name: "oss", Version: apiVersion})
	if code != 0 {
		return cli.Exit(T("MCP 服务异常退出", "MCP server exited abnormally"), code)
	}
	return nil
}
