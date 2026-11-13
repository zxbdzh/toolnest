package app

import (
	"fmt"
	"strconv"
	"strings"
)

func updateFailureText(code, detail string) string {
	if detail == "" {
		return ""
	}
	if status, err := strconv.Atoi(strings.TrimPrefix(code, "http-")); err == nil && strings.HasPrefix(code, "http-") {
		if status == 407 {
			return "代理认证失败（HTTP 407），请检查代理账号和密码。"
		}
		return updateEndpointStatusText(status)
	}
	messages := map[string]string{
		"network":        "无法连接更新服务。请检查网络或公共代理，然后重试。",
		"proxy":          "代理连接或认证失败，请检查地址、账号和密码。",
		"credentials":    "已保存的代理凭据不可用，请重新填写并保存。",
		"network-config": "公共代理配置无效，请修正后重试。",
		"signature":      "更新签名校验失败，未替换当前版本。请重新下载；持续失败时检查发布来源。",
		"archive":        "更新包格式不兼容或不完整，当前版本保持不变。",
		"size":           "下载内容不完整或大小不符，请重新下载。",
		"version":        "更新版本信息无效，当前版本保持不变。",
		"redirect":       "更新下载重定向未通过安全检查，未继续下载。",
		"permission":     "更新文件无法写入，请检查目录权限或占用后重试。",
		"missing-file":   "准备文件已不存在，请重新检查并下载更新。",
		"blocked":        "此版本曾启动失败，已停止自动尝试。确认后才能再次下载重试。",
		"startup":        "新版未能正常启动，已尝试恢复原版本。请查看恢复结果后再决定是否重试。",
		"recovery":       "更新未能自动恢复。请保留当前文件，并按恢复位置检查旧版副本。",
		"other":          "更新未完成，当前版本和数据不会被主动清理。请查看详情后重试。",
	}
	if message, ok := messages[code]; ok {
		return message
	}
	return updateErrorText(detail)
}

func updateEndpointStatusText(status int) string {
	switch status {
	case 404:
		return "HTTP 404，更新仓库或最新发行版不存在／不可公开访问；这不代表代理失效。"
	case 401:
		return "HTTP 401，更新服务要求认证；这不代表代理失效。"
	case 403, 429:
		return fmt.Sprintf("HTTP %d，更新服务拒绝访问或请求受限；可能是访问权限或频率限制，不应直接判定代理失效。", status)
	case 500, 502, 503, 504:
		return fmt.Sprintf("更新服务暂不可用（HTTP %d），请稍后重试，无需先更改代理。", status)
	default:
		return fmt.Sprintf("HTTP %d，更新服务未返回可用响应；已收到 HTTP 响应，不应直接判定代理失效。", status)
	}
}

func updateErrorText(message string) string {
	translations := map[string]string{
		"update source and production public key are not configured":           "更新源与签名公钥尚未配置，可继续使用离线 PATH 功能。",
		"update signature is invalid":                                          "更新签名校验失败，已拒绝替换；请重新检查版本来源。",
		"no verified update is ready":                                          "尚无已校验的更新，请先检查并下载。",
		"network request failed":                                               "网络请求失败，请检查公共代理或网络连接后重试。",
		"proxy connection or authentication failed":                            "代理连接或认证失败，请检查地址、账号和密码。",
		"update or application write is busy":                                  "另一项更新或关键写入正在执行，请完成后重试。",
		"updated application did not confirm startup":                          "新版未能完成启动确认，已尝试恢复原版本；请查看保留副本与错误详情。",
		"release blocked after an unsuccessful startup; manual retry required": "此版本曾启动失败，已停止自动尝试；需明确确认后重试。",
	}
	if translated, exists := translations[message]; exists {
		return translated
	}
	return message
}
