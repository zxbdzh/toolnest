package ui

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"toolnest/internal/app"
)

type pathPane struct {
	query    string
	selected int
}

func filterEntries(entries []app.Entry, query string) []app.Entry {
	query = strings.ToLower(query)
	filtered := make([]app.Entry, 0, len(entries))
	for _, entry := range entries {
		// 搜索仅决定可见性，保留空项、重复项和注册表中的原始序号。
		if strings.Contains(strings.ToLower(entry.Text), query) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func entryText(entry app.Entry) string {
	if entry.Text == "" {
		return "（空条目）"
	}
	return entry.Text
}

func scopeSummary(scope app.Scope) string {
	if !scope.Exists {
		return "注册表中不存在 PATH 值"
	}
	return fmt.Sprintf("%s · %d 项", scope.Type, len(scope.Entries))
}

func displayChanges(before, after app.Scope, scope string) []app.Change {
	var changes []app.Change
	if before.Exists != after.Exists {
		changes = append(changes, app.Change{Kind: "存在性", Scope: scope, Before: strconv.FormatBool(before.Exists), After: strconv.FormatBool(after.Exists)})
	}
	if before.Type != after.Type {
		changes = append(changes, app.Change{Kind: "注册表类型", Scope: scope, Before: before.Type, After: after.Type})
	}
	length := max(len(before.Entries), len(after.Entries))
	for position := 0; position < length; position++ {
		change := app.Change{Kind: fmt.Sprintf("原始位置 %d", position+1), Scope: scope, Before: "（无此项）", After: "（无此项）"}
		if position < len(before.Entries) {
			change.Before = entryText(before.Entries[position])
		}
		if position < len(after.Entries) {
			change.After = entryText(after.Entries[position])
		}
		if position >= len(before.Entries) || position >= len(after.Entries) || before.Entries[position].Text != after.Entries[position].Text {
			changes = append(changes, change)
		}
	}
	// Text 用于展示而非恢复；即便条目列表不完整，也不能把可见差异误报为无变化。
	if len(changes) == 0 && before.Text != after.Text {
		changes = append(changes, app.Change{Kind: "原值", Scope: scope, Before: before.Text, After: after.Text})
	}
	if before.RawDigest != "" && after.RawDigest != "" && before.RawDigest != after.RawDigest {
		changes = append(changes, app.Change{Kind: "原始字节", Scope: scope, Before: fmt.Sprintf("%d 字节 · %s", before.RawSize, before.RawDigest), After: fmt.Sprintf("%d 字节 · %s", after.RawSize, after.RawDigest)})
	}
	return changes
}

func validateDraft(settings app.Settings) error {
	if settings.ScheduleEnabled {
		switch settings.ScheduleMode {
		case "interval":
			if settings.IntervalHours < 1 || settings.IntervalHours > 168 {
				return fmt.Errorf("间隔必须为 1–168 的整数小时")
			}
		case "daily":
			if settings.DailyHour < 0 || settings.DailyHour > 23 || settings.DailyMinute < 0 || settings.DailyMinute > 59 {
				return fmt.Errorf("每日时间必须在 00:00–23:59 之间")
			}
		default:
			return fmt.Errorf("请选择有效的计划模式")
		}
	}
	if settings.ProxyMode == "custom" {
		address, err := url.Parse(settings.ProxyURL)
		if err != nil || address.Hostname() == "" || (address.Scheme != "http" && address.Scheme != "socks5") {
			return fmt.Errorf("自定义代理须为 http:// 或 socks5:// 地址")
		}
		port, err := strconv.Atoi(address.Port())
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("代理地址需要 1–65535 的有效端口")
		}
		if address.User != nil || address.RawQuery != "" || address.Fragment != "" || (address.Path != "" && address.Path != "/") {
			return fmt.Errorf("代理地址只填写协议、主机和端口，账号密码请分开填写")
		}
	}
	return nil
}

func updateLabel(state string) string {
	labels := map[string]string{
		"": "尚未检查", "unchecked": "尚未检查", "checking": "正在检查",
		"latest": "已是最新版本", "available": "发现新版本", "downloading": "正在下载",
		"verifying": "正在校验", "ready": "更新已就绪", "waiting": "等待安全切换",
		"failed": "更新失败", "error": "更新失败", "disabled": "更新源未配置", "unconfigured": "更新源或签名公钥未配置",
		"waiting-safe-switch": "等待安全切换", "applying": "正在切换版本", "pending-startup-confirmation": "等待新版启动确认",
		"confirmed": "新版启动已确认", "blocked": "新版启动失败，已停止自动尝试",
	}
	if label, ok := labels[state]; ok {
		return label
	}
	return state
}

func operationLabel(value string) string {
	labels := map[string]string{"manual": "手动", "automatic": "自动", "catch-up": "补跑", "retry": "失败重试", "before-restore": "恢复前保护", "imported": "旧版导入", "success": "成功", "unchanged": "无变化", "cancelled": "已取消", "failed": "失败", "partial": "部分完成", "rolled-back": "已回滚"}
	if label, ok := labels[value]; ok {
		return label
	}
	return value
}

func persistedSettings(settings app.Settings) app.Settings {
	// 服务不会回传系统凭据里的密码，确认保存时必须按公开配置比较。
	settings.ProxyPassword = ""
	return settings
}

func sameSchedule(first, second app.Settings) bool {
	return first.ScheduleEnabled == second.ScheduleEnabled && first.ScheduleMode == second.ScheduleMode &&
		first.IntervalHours == second.IntervalHours && first.DailyHour == second.DailyHour && first.DailyMinute == second.DailyMinute
}
