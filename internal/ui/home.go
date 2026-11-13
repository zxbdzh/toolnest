package ui

import (
	"fmt"
	"sort"
	"strings"

	native "github.com/egoist/mygo/ui"

	"toolnest/internal/app"
)

func (controller *Controller) homePage(context *native.Context, width float32) {
	theme := context.Theme()
	native.Scroll(context.Key("home-scroll")).Grow(1).MinHeight(0).Children(func() {
		native.Column(context.Key("home-content")).FillWidth().MaxWidth(1320).AlignSelf(native.Center).Padding(22, 30, 20, 30).Gap(18).Children(func() {
			native.Row(context).Gap(20).Children(func() {
				native.Column(context).Grow(1).Gap(6).Children(func() {
					native.Text(context, "工具空间").FontSize(26).FontWeight(600)
					native.Text(context, "环境、快照与连接，各就其位。").FontSize(13).TextColor(theme.TextMuted)
				})
				if width >= 900 {
					count, recent := validSnapshotSummary(controller.view.Snapshots)
					native.Column(context).Gap(5).Children(func() {
						label := "先为当前环境留一份快照"
						if count > 0 {
							label = fmt.Sprintf("已保留 %d 份可用快照", count)
						}
						native.Text(context, label).FontSize(13).TextColor(mistLink(theme))
						if recent.CreatedAt != "" {
							native.Text(context, "最近保存 · "+recent.CreatedAt).FontSize(12).TextColor(theme.TextMuted)
						}
					})
				}
			})
			controller.homeFeature(context, width)
			if count, _ := validSnapshotSummary(controller.view.Snapshots); count == 0 {
				native.Row(context.Key("first-backup-guide")).FillWidth().Padding(12, 16).Gap(12).Background(theme.Surface).Radius(8).Children(func() {
					native.Text(context, "还没有可恢复的快照。先保存当前 PATH，再查看快照或按需设置自动备份。").FontSize(13).Grow(1)
					if native.Button(context, "创建首份快照").Height(34).Disabled(controller.busy()).Clicked() {
						controller.launch("创建首份快照", controller.actions.Backup)
					}
				})
			}
			native.Row(context).Gap(12).Children(func() {
				native.Text(context, "常用工具").FontSize(17).FontWeight(600)
				native.Spacer(context)
				native.SearchField(context.Key("tool-search"), &controller.homeQuery).Label("搜索工具").Placeholder("搜索工具…").Width(220).Height(34).FontSize(13).Background(panelColor(theme)).Border(1, theme.Border).Radius(10)
			})
			controller.homeTools(context, width)
			controller.homeActivity(context, width)
		})
	})
}

func (controller *Controller) homeFeature(context *native.Context, width float32) {
	theme := context.Theme()
	height := float32(190)
	if width < 780 {
		height = 214
	}
	native.Row(context.Key("home-feature")).FillWidth().Height(height).Radius(18).Background(mistSurface(theme, "#e6eef5", "#2c4051")).Border(1, theme.Border).Shadow(0, 3, 12, 0, native.Hex("#24435c").Alpha(0.04)).AlignItems(native.Stretch).Children(func() {
		native.Column(context).Grow(1).MinWidth(0).Padding(20, 24).Gap(8).Children(func() {
			native.Text(context, "PATH 保护").FontSize(12).FontWeight(600).TextColor(mistLink(theme))
			titleSize := float32(24)
			if width < 780 {
				titleSize = 21
			}
			native.Text(context, "给下一次改变，留好退路。").FontSize(titleSize).FontWeight(600)
			native.Text(context, "安装新工具前，先保存一份可恢复的快照。").FontSize(13).TextColor(theme.TextMuted)
			native.Spacer(context)
			native.Row(context).Gap(8).Children(func() {
				if native.PrimaryButton(context.Key("home-backup"), "备份当前环境").Label("备份当前环境").Height(38).FontSize(13).Disabled(controller.busy()).Clicked() {
					controller.launch("创建快照", controller.actions.Backup)
				}
				if native.Button(context.Key("home-open-path"), "查看 PATH").Height(38).FontSize(13).Border(0, native.Transparent).Background(panelColor(theme).Alpha(0.65)).Clicked() {
					controller.page, controller.tab = "path", 0
				}
			})
		})
		if width >= 1100 {
			native.Column(context).Width(210).Justify(native.Center).AlignItems(native.Center).Children(func() {
				native.Image(context, folderIllustration).Width(210).Height(162)
			})
		}
		planWidth := float32(264)
		if width < 780 {
			planWidth = 186
		}
		native.Column(context.Key("home-plan")).Width(planWidth).Margin(8).Padding(16).Gap(8).Background(panelColor(theme).Alpha(0.75)).Radius(12).Children(func() {
			native.Row(context).Gap(8).Children(func() {
				native.Icon(context, clockIcon).Width(19).Height(19).TextColor(mistLink(theme))
				native.Text(context, "自动备份").FontSize(14).FontWeight(600).Grow(1)
			})
			native.Spacer(context)
			status, detail := scheduleSummary(controller.view)
			statusSize := float32(17)
			if len([]rune(status)) > 12 {
				statusSize = 12
			}
			native.Text(context, status).FontSize(statusSize).FontWeight(600).TextColor(mistLink(theme)).SingleLine().Tooltip(status)
			native.Text(context, detail).FontSize(12).TextColor(theme.TextMuted)
			native.Spacer(context)
			if native.Button(context.Key("home-schedule"), "管理备份计划").Height(32).FontSize(13).Border(0, native.Transparent).Background(theme.Surface).TextColor(mistLink(theme)).Clicked() {
				controller.page, controller.tab = "path", 2
			}
		})
	})
}

type homeTool struct {
	id, title, description, detail string
	icon                           *native.SVG
}

func (controller *Controller) homeTools(context *native.Context, width float32) {
	count, _ := validSnapshotSummary(controller.view.Snapshots)
	tools := []homeTool{
		{"path", "PATH 保护", "查看与复制环境路径", fmt.Sprintf("用户 %d 项 · 系统 %d 项", len(controller.view.User.Entries), len(controller.view.System.Entries)), pathIcon},
		{"snapshots", "快照时光机", "比较变化，找回之前的环境", fmt.Sprintf("%d 份可用快照 · 手动清理", count), archiveIcon},
		{"network", "公共代理", "统一管理工具箱的网络连接", proxySummary(controller.view.Settings.ProxyMode), networkIcon},
	}
	query := strings.ToLower(strings.TrimSpace(controller.homeQuery))
	visible := make([]homeTool, 0, len(tools))
	for _, tool := range tools {
		if strings.Contains(strings.ToLower(tool.title+tool.description+tool.detail), query) {
			visible = append(visible, tool)
		}
	}
	if len(visible) == 0 {
		emptyState(context, "没有匹配的工具", "试试 PATH、快照或代理；搜索不会改变任何设置。")
		return
	}
	row := native.Row(context.Key("tool-cards")).FillWidth().Gap(14).AlignItems(native.Stretch)
	if width < 780 {
		row.Column().Gap(12)
	}
	row.Children(func() {
		for _, tool := range visible {
			controller.homeCard(context, tool, width < 780)
		}
	})
}

func (controller *Controller) homeCard(context *native.Context, tool homeTool, compact bool) {
	theme := context.Theme()
	card := native.Button(context.Key("tool-"+tool.id), "").Label("打开"+tool.title).Column().Padding(18).Radius(14).Background(panelColor(theme)).Border(1, theme.Border.Alpha(0.75)).Gap(14).AlignItems(native.Stretch)
	if compact {
		card.FillWidth().Height(136)
	} else {
		card.Grow(1).MinWidth(0).Height(148)
	}
	card.Children(func() {
		native.Row(context).Grow(1).Gap(12).Children(func() {
			iconTile(context, tool.icon, 40)
			native.Column(context).Grow(1).MinWidth(0).Gap(6).Children(func() {
				native.Text(context, tool.title).FontSize(16).FontWeight(600)
				native.Text(context, tool.description).FontSize(12).TextColor(theme.TextMuted)
			})
		})
		divider(context)
		native.Row(context).Gap(8).Children(func() {
			native.Text(context, tool.detail).FontSize(12).TextColor(theme.TextMuted).Grow(1).MinWidth(0).SingleLine().Tooltip(tool.detail)
			native.Icon(context, arrowIcon).Width(16).Height(16).TextColor(mistLink(theme))
		})
	})
	if card.Hovered() {
		card.Border(1, mistLink(theme).Alpha(0.45)).Background(mistSurface(theme, "#f9fcff", "#304251"))
	}
	if card.Clicked() {
		switch tool.id {
		case "network":
			controller.page, controller.settingsSection = "settings", 2
		case "snapshots":
			controller.page, controller.tab = "path", 1
		default:
			controller.page, controller.tab = "path", 0
		}
	}
}

func (controller *Controller) homeActivity(context *native.Context, width float32) {
	theme := context.Theme()
	panel(context.Key("home-activity")).Padding(16, 20).Gap(12).Children(func() {
		native.Row(context).Gap(10).Children(func() {
			native.Icon(context, clockIcon).Width(18).Height(18).TextColor(mistLink(theme))
			native.Text(context, "最近活动").FontSize(14).FontWeight(600).Grow(1)
			if native.Button(context, "执行记录").Height(28).FontSize(12).Border(0, native.Transparent).Background(native.Transparent).TextColor(mistLink(theme)).Clicked() {
				controller.page, controller.tab = "path", 2
			}
		})
		if len(controller.view.Records) == 0 {
			native.Text(context, "还没有操作记录。创建快照后，可以在这里查看结果。").FontSize(13).TextColor(theme.TextMuted)
			return
		}
		// 排序只操作视图副本，不改变执行记录或把尚未完成的操作标成成功。
		records := append([]app.Record(nil), controller.view.Records...)
		sort.SliceStable(records, func(first, second int) bool { return records[first].At > records[second].At })
		for _, record := range records[:min(3, len(records))] {
			native.Row(context).Gap(12).Children(func() {
				native.Text(context, operationLabel(record.Outcome)).FontSize(12).Width(60).TextColor(mistLink(theme))
				native.Text(context, record.Message).FontSize(13).Grow(1).MinWidth(0).SingleLine().Tooltip(record.Message)
				if width >= 900 {
					native.Text(context, record.At).FontSize(12).TextColor(theme.TextMuted).SingleLine()
				}
			})
		}
	})
}

func (controller *Controller) footer(context *native.Context) {
	theme := context.Theme()
	native.Row(context.Key("footer")).FillWidth().Height(28).Padding(0, 26).Gap(8).Background(theme.Surface).Children(func() {
		native.Icon(context, pathIcon).Width(12).Height(12).TextColor(theme.TextMuted)
		native.Text(context, "本机工具箱 · PATH 原值只读查看").FontSize(11).TextColor(theme.TextMuted).Grow(1)
		native.Text(context, "ToolNest "+controller.view.Version).FontSize(11).TextColor(theme.TextMuted).SingleLine()
	})
}

func validSnapshotSummary(snapshots []app.Snapshot) (int, app.Snapshot) {
	count := 0
	var latest app.Snapshot
	for _, snapshot := range snapshots {
		// 损坏文件保留在历史列表供诊断，但不能算作可用保护或冒充成功状态。
		if snapshot.Error != "" {
			continue
		}
		count++
		if snapshot.CreatedAt > latest.CreatedAt {
			latest = snapshot
		}
	}
	return count, latest
}

func scheduleSummary(view app.View) (string, string) {
	if !view.Settings.ScheduleEnabled {
		return "尚未开启", "开启后仅在环境变化时保存"
	}
	frequency := fmt.Sprintf("每 %d 小时 · 仅变化时保存", view.Settings.IntervalHours)
	if view.Settings.ScheduleMode == "daily" {
		frequency = fmt.Sprintf("每天 %02d:%02d · 本地时间", view.Settings.DailyHour, view.Settings.DailyMinute)
	}
	if view.NextRun == "" {
		return "等待安排", frequency
	}
	return view.NextRun, frequency
}

func proxySummary(mode string) string {
	switch mode {
	case "custom":
		return "自定义代理 · 仅用于工具箱"
	case "direct":
		return "直接连接 · 系统代理不变"
	default:
		return "跟随系统 · 统一连接设置"
	}
}

var clockIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>`))
var arrowIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14m-6-6 6 6-6 6"/></svg>`))

// 插画是随程序内嵌的装饰，不参与状态判断；由 MyGo SVG 绘制器适配实际 DPI。
var folderIllustration = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="340" height="220" viewBox="0 0 340 220" fill="none"><defs><linearGradient id="folder" x1="90" y1="60" x2="210" y2="200" gradientUnits="userSpaceOnUse"><stop stop-color="#a5c0d3"/><stop offset="1" stop-color="#597f9c"/></linearGradient><linearGradient id="front" x1="130" y1="105" x2="190" y2="195" gradientUnits="userSpaceOnUse"><stop stop-color="#c4d8e6"/><stop offset="1" stop-color="#87a8c0"/></linearGradient></defs><ellipse cx="177" cy="194" rx="94" ry="9" fill="#4d6378" opacity=".09"/><rect x="98" y="47" width="145" height="130" rx="13" transform="rotate(9 98 47)" fill="#ffffff" opacity=".65"/><rect x="110" y="37" width="141" height="130" rx="13" transform="rotate(-6 110 37)" fill="#f7fbfd"/><path d="m131 72 18 0m-18 13h78m-78 13h59" stroke="#cfdde7" stroke-width="6" stroke-linecap="round"/><path d="M83 94a13 13 0 0 1 13-13h45l17 17h91a13 13 0 0 1 13 13v62a13 13 0 0 1-13 13H96a13 13 0 0 1-13-13Z" fill="url(#folder)"/><path d="M76 117a11 11 0 0 1 11-11h171a11 11 0 0 1 11 13l-13 64a12 12 0 0 1-12 10H96a12 12 0 0 1-12-10Z" fill="url(#front)" stroke="#dce9f2"/><rect x="146" y="130" width="52" height="39" rx="9" fill="#ffffff" opacity=".3"/><path d="m156 142 7 6-7 6m14 0h14" stroke="#ffffff" stroke-width="4" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
var snapshotIllustration = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="340" height="220" viewBox="0 0 340 220" fill="none"><ellipse cx="170" cy="189" rx="73" ry="9" fill="#4d6378" opacity=".1"/><rect x="107" y="46" width="123" height="126" rx="13" transform="rotate(13 107 46)" fill="#b0c0ac"/><rect x="94" y="53" width="123" height="126" rx="13" transform="rotate(-8 94 53)" fill="#cdd7c6" stroke="#f5f8f1"/><rect x="106" y="63" width="127" height="124" rx="13" fill="#f5f8f1" stroke="#cdd7c6"/><circle cx="170" cy="120" r="31" fill="#e3ebdd"/><path d="M170 99v22l14 8" stroke="#879d81" stroke-width="4" stroke-linecap="round"/><path d="M139 165h62" stroke="#cdd7c6" stroke-width="5" stroke-linecap="round"/><circle cx="226" cy="71" r="17" fill="#728d70"/><polyline points="218 71 224 77 234 65" stroke="#ffffff" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
var networkIllustration = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="340" height="220" viewBox="0 0 340 220" fill="none"><ellipse cx="170" cy="189" rx="76" ry="9" fill="#4d6378" opacity=".1"/><circle cx="170" cy="116" r="63" fill="#ece5da" stroke="#d1c1aa" stroke-width="2"/><ellipse cx="170" cy="116" rx="28" ry="63" stroke="#d1c1aa" stroke-width="2"/><path d="M107 116h126m-113-33h106m-106 66h106" stroke="#d1c1aa" stroke-width="2"/><path d="m99 154 36-20 60 18 48-61" stroke="#ffffff" stroke-width="10" stroke-linecap="round" stroke-linejoin="round"/><path d="m99 154 36-20 60 18 48-61" stroke="#a18b6d" stroke-width="4" stroke-linecap="round" stroke-linejoin="round"/><circle cx="99" cy="154" r="7" fill="#a18b6d"/><circle cx="243" cy="91" r="9" fill="#a18b6d" stroke="#ffffff" stroke-width="4"/></svg>`))
