package ui

import (
	"fmt"
	"sort"
	"strings"

	native "github.com/egoist/mygo/ui"

	"toolnest/internal/app"
)

func (controller *Controller) homePage(context *native.Context, width float32) {
	native.Scroll(context.Key("home-scroll")).Grow(1).MinHeight(0).Children(func() {
		native.Column(context.Key("home-content")).FillWidth().MaxWidth(1280).AlignSelf(native.Center).
			Padding(4, contentInset(width), 16, contentInset(width)).Gap(16).Children(func() {
			controller.homeProtection(context)
			// 每列在所属分支里直接构建；提前复用 Element 会挂到错误父节点，
			// 使活动栏覆盖快照列表。主列有弹性宽度，右侧只承接辅助信息。
			if width >= 840 {
				native.Row(context).FillWidth().Gap(20).AlignItems(native.Start).Children(func() {
					native.Column(context.Key("home-recent-column")).Grow(1).Basis(0).MinWidth(0).Children(func() {
						controller.recentSnapshots(context)
					})
					native.Column(context.Key("home-activity-column")).Width(264).Children(func() {
						controller.homeActivity(context)
					})
				})
			} else {
				native.Column(context).FillWidth().Gap(20).Children(func() {
					native.Column(context).FillWidth().MinWidth(0).Children(func() { controller.recentSnapshots(context) })
					native.Column(context).FillWidth().Children(func() { controller.homeActivity(context) })
				})
			}
			controller.currentPathsPreview(context)
		})
	})
}

// 首页的第一屏只回答“现在能否恢复，以及下一步做什么”；
// 不用三块指标卡，也不把三个入口做成一样重的按钮。
func (controller *Controller) homeProtection(context *native.Context) {
	theme := context.Theme()
	count, recent := validSnapshotSummary(controller.view.Snapshots)
	summary := "还没有备份，先保存当前 PATH"
	if count > 0 {
		summary = fmt.Sprintf("%d 份可用快照 · 最近 %s", count, displayTime(recent.CreatedAt))
	}
	native.Column(context).FillWidth().BorderWidth(0, 0, 1, 0).BorderColor(theme.Border.Alpha(0.5)).
		Padding(4, 0, 12, 0).Gap(10).Children(func() {
		native.Row(context).Gap(12).AlignItems(native.Center).Children(func() {
			native.Row(context).Width(40).Height(40).Radius(13).AlignItems(native.Center).Justify(native.Center).
				Background(theme.Accent.Alpha(0.1)).Children(func() {
				native.Icon(context, shieldIcon).Width(23).Height(23).TextColor(theme.Accent)
			})
			native.Column(context).Grow(1).MinWidth(0).Gap(4).Children(func() {
				native.Text(context, "当前环境").FontSize(sectionSize(context)).FontWeight(500)
				native.Text(context, summary).FontSize(controlTextSize(context)).TextColor(theme.TextMuted).SingleLine().Tooltip(summary)
			})
			if primaryButton(context, "立即备份", controlOptions{key: "home-backup", label: "备份当前环境", icon: backupIcon, disabled: controller.busy()}).Clicked() {
				controller.launch("创建快照", controller.actions.Backup)
			}
		})
		native.Row(context).Gap(14).AlignItems(native.Center).Wrap().Children(func() {
			native.Textf(context, "用户 PATH · %d 项", len(controller.view.User.Entries)).FontSize(controlTextSize(context))
			native.Textf(context, "系统 PATH · %d 项", len(controller.view.System.Entries)).FontSize(controlTextSize(context))
			status, frequency := scheduleSummary(controller.view)
			if controller.view.Settings.ScheduleEnabled {
				status += " · " + frequency
			}
			native.Text(context, "自动备份 · "+status).FontSize(controlTextSize(context)).TextColor(theme.TextMuted).Grow(1).MinWidth(0).SingleLine().Tooltip(status)
			if quietButton(context, "查看 PATH", controlOptions{icon: chevronRightIcon}).Clicked() {
				controller.page, controller.tab = "path", 0
			}
			if quietButton(context, "管理计划", controlOptions{label: "管理备份计划", icon: chevronRightIcon}).Clicked() {
				controller.page, controller.tab = "path", 2
			}
		})
	})
}

func (controller *Controller) recentSnapshots(context *native.Context) {
	native.Column(context.Key("recent-snapshots")).FillWidth().MinWidth(0).Children(func() {
		count, _ := validSnapshotSummary(controller.view.Snapshots)
		title := "最近快照"
		if count > 0 {
			title += fmt.Sprintf(" · %d", count)
		}
		sectionHeader(context, title, nil, "全部快照", func() { controller.page, controller.tab = "path", 1 })
		controller.recentSnapshotRows(context)
	})
}

func (controller *Controller) recentSnapshotRows(context *native.Context) {
	theme := context.Theme()
	native.Column(context).FillWidth().Children(func() {
		snapshots := append([]app.Snapshot(nil), controller.view.Snapshots...)
		sort.SliceStable(snapshots, func(first, second int) bool { return snapshots[first].CreatedAt > snapshots[second].CreatedAt })
		shown := 0
		rows := native.Column(context.Key("recent-snapshot-rows")).FillWidth().Gap(4)
		for _, snapshot := range snapshots {
			if snapshot.Error != "" || shown == 3 {
				continue
			}
			shown++
			rows.Children(func() {
				button := native.ButtonBase(context.Key(snapshot.ID)).Label("打开最近快照 "+snapshot.ID).
					FillWidth().Height(56).Padding(7, 10).Gap(10).Radius(11).
					AlignItems(native.Center).FocusRing(false).Transition(colorTransition(context))
				if button.Hovered() {
					button.Background(theme.SurfaceHover)
				}
				if button.Pressed() {
					button.Background(theme.SurfacePressed)
				}
				button.Children(func() {
					native.Row(context).Width(32).Height(32).Radius(10).Justify(native.Center).AlignItems(native.Center).
						Background(theme.Accent.Alpha(0.07)).Children(func() {
						native.Icon(context, archiveIcon).Width(17).Height(17).TextColor(theme.Accent)
					})
					native.Column(context).Grow(1).MinWidth(0).Gap(2).Children(func() {
						native.Text(context, displayTime(snapshot.CreatedAt)).FontSize(controlTextSize(context)).FontWeight(500).SingleLine().Tooltip(snapshot.CreatedAt)
						native.Text(context, operationLabel(snapshot.Source)+" · "+snapshot.Summary).FontSize(detailSize(context)).TextColor(theme.TextMuted).SingleLine().Tooltip(snapshot.Summary)
					})
					native.Icon(context, chevronRightIcon).Width(15).Height(15).TextColor(theme.TextMuted)
				})
				if button.Clicked() {
					controller.openSnapshot(snapshot.ID)
				}
			})
		}
		rows.Children(func() {
			if shown == 0 {
				native.Column(context).FillWidth().Gap(4).Padding(10, 2).Children(func() {
					native.Text(context, "还没有快照").FontSize(controlTextSize(context)).FontWeight(500)
					native.Text(context, "备份后可在这里比较和恢复 PATH。").FontSize(detailSize(context)).TextColor(theme.TextMuted)
				})
			}
			if count, _ := validSnapshotSummary(snapshots); count < len(snapshots) {
				native.Text(context, "部分快照无法读取，可在「全部快照」中查看原因。").FontSize(detailSize(context)).TextColor(theme.Warning).Padding(6, 2)
			}
		})
	})
}

func (controller *Controller) currentPathsPreview(context *native.Context) {
	theme := context.Theme()
	native.Column(context.Key("current-paths-preview")).FillWidth().MinWidth(0).Children(func() {
		sectionHeader(context, "当前 PATH", nil, "查看完整列表", func() { controller.page, controller.tab = "path", 0 })
		rows := native.Column(context).FillWidth().Background(panelColor(theme).Alpha(0.38)).
			Border(1, theme.Border.Alpha(0.45)).Radius(panelRadius).Dividers(1, theme.Border.Alpha(0.4)).Clip()
		shown := 0
		for _, scope := range []struct {
			label string
			value app.Scope
		}{{"用户", controller.view.User}, {"系统", controller.view.System}} {
			for _, entry := range scope.value.Entries {
				if shown >= 5 {
					break
				}
				shown++
				label, value := scope.label, entryText(entry)
				rows.Children(func() {
					native.Row(context).MinHeight(38).Gap(9).AlignItems(native.Center).Padding(6, 14).Children(func() {
						native.Textf(context, "%s %02d", label, entry.Index).FontSize(detailSize(context)).FontWeight(500).TextColor(theme.TextMuted).Width(52)
						native.Text(context, value).Font("Cascadia Mono").FontSize(controlTextSize(context)).SingleLine().Grow(1).MinWidth(0).Tooltip(value)
					})
				})
			}
		}
		if shown == 0 {
			rows.Children(func() {
				native.Text(context, "没有可显示的 PATH 条目").FontSize(controlTextSize(context)).TextColor(theme.TextMuted).Padding(10, 2)
			})
		}
	})
}

func sectionHeader(context *native.Context, title string, icon *native.SVG, action string, onClick func()) {
	native.Row(context).Height(32).Gap(8).Margin(0, 0, 4, 0).AlignItems(native.Center).Children(func() {
		if icon != nil {
			native.Icon(context, icon).Width(16).Height(16).TextColor(context.Theme().TextMuted)
		}
		native.Text(context, title).FontSize(sectionSize(context)).FontWeight(500).Grow(1).MinWidth(0)
		if action != "" {
			if compactAction(context, action, controlOptions{label: action, icon: chevronRightIcon}).Clicked() {
				onClick()
			}
		}
	})
}

func (controller *Controller) homeActivity(context *native.Context) {
	theme := context.Theme()
	native.Column(context.Key("home-activity")).FillWidth().MinWidth(0).Children(func() {
		sectionHeader(context, "最近活动", nil, "查看记录", func() { controller.page, controller.tab = "path", 3 })
		rows := listRows(context, "home-activity-rows").Background(panelColor(theme).Alpha(0.35)).
			Border(1, theme.Border.Alpha(0.35)).Radius(panelRadius).Clip()
		if len(controller.view.Records) == 0 {
			rows.Children(func() {
				native.Text(context, "暂无活动").FontSize(controlTextSize(context)).TextColor(theme.TextMuted).Padding(12, 2)
			})
			return
		}
		records := append([]app.Record(nil), controller.view.Records...)
		sort.SliceStable(records, func(first, second int) bool { return records[first].At > records[second].At })
		for _, record := range records[:min(4, len(records))] {
			rows.Children(func() {
				native.Row(context).MinHeight(52).Gap(8).AlignItems(native.Center).Padding(8, 12).Children(func() {
					color := theme.TextMuted
					if record.Outcome == "failed" || record.Outcome == "partial" {
						color = theme.Danger
					}
					native.Icon(context, activityIcon(record.Outcome)).Width(15).Height(15).TextColor(color)
					native.Column(context).Grow(1).MinWidth(0).Gap(2).Children(func() {
						message := record.Message
						if message == "" {
							message = record.Operation
						}
						native.Text(context, message).FontSize(controlTextSize(context)).SingleLine().Tooltip(message)
						native.Text(context, displayTime(record.At)+" · "+operationLabel(record.Outcome)).FontSize(detailSize(context)).TextColor(color).SingleLine()
					})
				})
			})
		}
	})
}

func validSnapshotSummary(snapshots []app.Snapshot) (int, app.Snapshot) {
	count := 0
	var latest app.Snapshot
	for _, snapshot := range snapshots {
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
		return "尚未开启", "可按需开启，环境有变化时自动保存"
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

func scopeIcon(label string) *native.SVG {
	if strings.Contains(label, "系统") {
		return systemScopeIcon
	}
	return userScopeIcon
}

func activityIcon(outcome string) *native.SVG {
	if outcome == "failed" || outcome == "partial" {
		return shieldIcon
	}
	return checkIcon
}

var clockIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>`))
var arrowIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14m-6-6 6 6-6 6"/></svg>`))
