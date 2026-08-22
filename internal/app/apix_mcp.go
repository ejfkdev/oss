package app

import (
	"context"
	"strings"
	"time"

	errs "github.com/ejfkdev/xyz-go/errors"
	"github.com/ejfkdev/xyz-go/mcp"
	"github.com/ejfkdev/xyz-go/registry"
	"github.com/ejfkdev/xyz-go/spec"
)

// mcpArgs starts the MCP tool server (stdio / SSE / streamable HTTP) backed by
// the same registry that powers serve. CLI-only.
type mcpArgs struct {
	Transport      string        `json:"transport,omitempty" desc:"传输 stdio|http|sse / transport" cli:"positional"`
	Addr           string        `json:"addr,omitempty" desc:"监听地址（http/sse）/ listen address (http/sse)"`
	Versions       string        `json:"versions,omitempty" desc:"限定协议版本（逗号分隔）/ pin protocol versions"`
	Bearer         []string      `json:"bearer,omitempty" desc:"Bearer 令牌（可重复）/ bearer token(s)"`
	CORS           []string      `json:"cors,omitempty" desc:"CORS 允许来源 / CORS origins"`
	Name           string        `json:"name,omitempty" desc:"服务器名称 / server name"`
	ServerVersion  string        `json:"server-version,omitempty" desc:"服务器版本 / server version"`
	SessionTimeout time.Duration `json:"session-timeout,omitempty" desc:"会话空闲超时 / idle session expiry"`
	Stateless      bool          `json:"stateless,omitempty" desc:"流式 HTTP 无状态模式 / streamable HTTP stateless"`
	JSONResponse   bool          `json:"json-response,omitempty" desc:"流式 HTTP 以 JSON 应答 / streamable HTTP answers JSON"`
}

func registerCliMCP(reg *registry.Registry) error {
	_, err := spec.Define("mcp", apixMCP).
		Summary(T("启动 MCP 工具服务器（stdio/sse/http）", "start the MCP tool server (stdio/sse/http)")).
		Description(T("把 ls / stat / cat / presign / find 暴露为 MCP 工具（名称即命令名），供支持 MCP 的客户端（如 Claude）调用。cat 读取对象内容：UTF-8 文本放 text 字段、二进制放 base64，单次上限 16MiB。",
			"Exposes ls/stat/cat/presign/find as MCP tools (tool names = command names) for MCP-capable clients (Claude etc.). cat reads content: UTF-8 text in the text field, binary in base64, capped at 16MiB.")).
		CLI(spec.CliHints{
			Usage: "mcp <stdio|http|sse>",
			After: T(`示例:
   oss mcp stdio                             本地进程标准输入/输出（最常用）
   oss mcp http --addr :9000 --bearer tok    远程 streamable HTTP
   oss mcp sse --addr 127.0.0.1:9000         SSE 传输（局域网内可用）

客户端配置（Claude Desktop claude_desktop_config.json）:
   {"mcpServers": {"oss": {"command": "oss", "args": ["mcp", "stdio"]}}}`,
				`EXAMPLES:
   oss mcp stdio                             local process over stdio (most common)
   oss mcp http --addr :9000 --bearer tok    remote streamable HTTP
   oss mcp sse --addr 127.0.0.1:9000         SSE transport

Client configuration (Claude Desktop claude_desktop_config.json):
   {"mcpServers": {"oss": {"command": "oss", "args": ["mcp", "stdio"]}}}`),
		}).
		Register(reg)
	return err
}

func apixMCP(ctx context.Context, in *mcpArgs) (int, error) {
	switch in.Transport {
	case "stdio", "http", "sse":
	default:
		return 2, errs.New(errs.KindInvalidInput, T("用法: oss mcp <stdio|http|sse>", "usage: oss mcp <stdio|http|sse>"))
	}

	rid, err := BuildAPIRegistry()
	if err != nil {
		return 2, err
	}

	args := []string{in.Transport}
	add := func(name, value string) {
		if value != "" {
			args = append(args, "--"+name, value)
		}
	}
	add("addr", in.Addr)
	add("versions", in.Versions)
	if v := strings.Join(in.Bearer, ","); v != "" {
		add("bearer", v)
	}
	if v := strings.Join(in.CORS, ","); v != "" {
		add("cors", v)
	}
	add("name", in.Name)
	add("server-version", in.ServerVersion)
	if in.SessionTimeout > 0 {
		add("session-timeout", in.SessionTimeout.String())
	}
	if in.Stateless {
		args = append(args, "--stateless")
	}
	if in.JSONResponse {
		args = append(args, "--json-response")
	}

	code := mcp.RunContextWithOptions(ctx, rid, args, mcp.Options{
		Name: "oss", Version: apiVersion, Instructions: mcpInstructions,
	})
	if code != 0 {
		return code, errs.New(errs.KindInternal, T("MCP 服务异常退出", "MCP server exited abnormally"))
	}
	return 0, nil
}
