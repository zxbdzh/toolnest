package ui

import (
	"fmt"
	"strings"

	native "github.com/egoist/mygo/ui"
)

func (controller *Controller) dismissUpdate() {
	version := controller.view.Update.Available
	if controller.view.Update.State == "confirmed" {
		version = controller.view.Version
	}
	controller.dismissedUpdate = controller.view.Update.State + ":" + version
	if actions, ok := controller.actions.(interface{ DismissUpdate(string) }); ok {
		actions.DismissUpdate(version)
	}
}

func (controller *Controller) updateNotice(context *native.Context) {
	update := controller.view.Update
	message, version := "", update.Available
	switch update.State {
	case "ready", "waiting-safe-switch":
		message = "新版 " + version + " 已准备好 · 下次打开时生效，或现在重启"
	case "available":
		message = "发现新版 " + version + " · 查看变化并准备更新"
	case "confirmed":
		version = controller.view.Version
		message = "已更新至 " + version + " · 快照和设置继续保留"
	case "blocked", "failed":
		message = "更新未完成 · 当前版本继续可用，请查看处理方式"
		if update.RolledBack {
			message = "更新未完成，已回退至 " + controller.view.Version + " · 查看原因与处理方式"
		}
	}
	notice := update.State + ":" + version
	if message == "" || notice == controller.dismissedUpdate || notice == update.DismissedVersion {
		return
	}
	native.Row(context.Key("update-notice")).FillWidth().Padding(8, 24).Gap(8).Background(context.Theme().SurfaceHover).Children(func() {
		native.Icon(context, updateIcon).Width(18).Height(18).TextColor(mistLink(context.Theme()))
		native.Text(context, message).FontSize(12).Grow(1).MinWidth(0).SingleLine().Tooltip(message).Role(native.RoleStatus)
		if native.Button(context, "查看更新").Height(30).FontSize(12).Clicked() {
			controller.ShowUpdates()
		}
		if native.Button(context, "稍后").Label("稍后提醒更新").Height(30).FontSize(12).Clicked() {
			controller.dismissUpdate()
		}
	})
}

func (controller *Controller) exitNotice(context *native.Context) {
	if controller.view.PendingExit == "" {
		return
	}
	message := "正在等待当前操作完成，然后退出"
	if controller.view.PendingExit == "restart" {
		message = "正在等待当前操作完成，然后重启更新"
	}
	if controller.view.ExitCommitting {
		message = "正在保存并安全退出…"
	}
	native.Row(context.Key("exit-wait")).FillWidth().Padding(12, 24).Gap(12).Background(context.Theme().SurfaceHover).Children(func() {
		native.Text(context, message).FontSize(13).Role(native.RoleStatus).Grow(1)
		if actions, ok := controller.actions.(interface{ CancelExit() }); ok {
			if native.Button(context, "取消等待").Height(32).Disabled(controller.view.ExitCommitting).Clicked() {
				actions.CancelExit()
			}
		}
	})
}

func (controller *Controller) updateProgress(context *native.Context) {
	update := controller.view.Update
	phase := update.NetworkPhase
	if phase == "" {
		phase = update.State
	}
	if phase == "checking" {
		native.Text(context, "正在检查更新…").Role(native.RoleStatus).FontSize(13)
		return
	}
	if phase == "verifying" {
		native.Text(context, "下载完成，正在校验签名与文件…").Role(native.RoleStatus).FontSize(13)
		return
	}
	if phase != "downloading" {
		return
	}
	native.Progress(context, max(0, min(1, update.Progress))).FillWidth().Label("更新下载进度")
	message := "正在连接下载服务…"
	if update.Total > 0 {
		message = fmt.Sprintf("%.0f%% · %s / %s", max(0, min(1, update.Progress))*100, downloadSize(update.Downloaded), downloadSize(update.Total))
		if update.BytesPerSecond > 0 && update.Downloaded < update.Total {
			message += fmt.Sprintf(" · %s/s", downloadSize(int64(update.BytesPerSecond)))
		}
	}
	native.Text(context, message).FontSize(12).TextColor(context.Theme().TextMuted).Role(native.RoleStatus)
}

func downloadSize(bytes int64) string {
	if bytes >= 1<<20 {
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	}
	if bytes >= 1<<10 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/(1<<10))
	}
	return fmt.Sprintf("%d B", bytes)
}

func (controller *Controller) releaseNotes(context *native.Context) {
	native.Text(context, "本次更新").FontSize(16).FontWeight(600)
	notes := controller.view.Update.Notes
	if notes == "" {
		notes = "版本说明暂不可用，可以稍后重新检查。已经完成校验的文件会保留在本机。"
	}
	_, height := context.Size()
	native.Scroll(context.Key("release-notes")).Height(min(220, max(110, height*0.22))).Children(func() {
		native.Column(context).FillWidth().Gap(8).Children(func() {
			for _, line := range strings.Split(notes, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				heading := strings.HasPrefix(line, "#")
				line = strings.TrimSpace(strings.TrimLeft(line, "#"))
				if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
					line = "• " + line[2:]
				}
				text := native.Text(context, line).FontSize(13).Selectable()
				if heading {
					text.FontWeight(600).FontSize(15)
				}
			}
		})
	})
}
