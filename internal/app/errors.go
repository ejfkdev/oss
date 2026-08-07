package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/smithy-go"
)

// apiErr annotates S3 API errors with actionable hints and normalizes
// cancellation.
func apiErr(err error, anonymous bool) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.New(T("已中断", "interrupted"))
	}
	// A non-XML response (HTML page, proxy/portal interception, wrong
	// endpoint) surfaces as a deserialization failure.
	if strings.Contains(err.Error(), "deserialization failed") {
		return fmt.Errorf("%w\n"+T(
			"提示: 服务端响应不是有效的 S3 XML——请检查 endpoint 是否正确，或确认网络未被代理/防火墙改写",
			"hint: the server response is not valid S3 XML — check the endpoint, or that a proxy/firewall is not rewriting traffic"), err)
	}
	var ae smithy.APIError
	if errors.As(err, &ae) && anonymous {
		var resp interface{ HTTPStatusCode() int }
		status := 0
		if errors.As(err, &resp) {
			status = resp.HTTPStatusCode()
		}
		if status == 401 || status == 403 {
			return fmt.Errorf("%w\n"+T(
				"提示: 匿名访问被拒绝；请使用 --ak/--sk 重试（STS 场景加 --token）",
				"hint: anonymous access was denied; retry with --ak/--sk (and --token for STS)"), err)
		}
	}
	return err
}
