package ui

import (
	"fmt"

	native "github.com/egoist/mygo/ui"

	"toolnest/internal/app"
)

func field(context *native.Context, label string, content func()) {
	width, _ := context.Size()
	labelWidth := float32(136)
	if width < 780 {
		labelWidth = 108
	}
	native.Row(context.Key(label)).MinHeight(40).Gap(10).AlignItems(native.Center).Children(func() {
		native.Text(context, label).Width(labelWidth).FontSize(controlTextSize(context))
		native.Row(context).Grow(1).MinWidth(0).Gap(8).Wrap().AlignItems(native.Center).Children(content)
	})
}

func section(context *native.Context, title string) {
	native.Text(context, title).FontSize(sectionSize(context)).FontWeight(600).Margin(0, 0, 5, 0)
}

// 标题与字段共用起点，仅保留有用途的图标，不添加装饰性副标题。
func settingsGroup(context *native.Context, title string, icon *native.SVG) {
	native.Row(context).Height(26).Gap(7).Margin(0, 0, 2, 0).AlignItems(native.Center).Children(func() {
		if icon != nil {
			native.Icon(context, icon).Width(15).Height(15).TextColor(context.Theme().TextMuted)
		}
		native.Text(context, title).FontSize(detailSize(context)).FontWeight(500).TextColor(context.Theme().TextMuted).Grow(1).MinWidth(0)
	})
}

// 分类使用紧凑内嵌分段控件，与主题和字号选项保持同一按压、选中语言。
func settingsTabs(context *native.Context, selected *int, labels ...string) bool {
	before := *selected
	native.Row(context.Key("settings-sections")).FillWidth().Height(44).AlignItems(native.Center).Children(func() {
		segmentedControl(context, "settings-category", selected, labels...)
	})
	return *selected != before
}

func choice(context *native.Context, key string, target *string, labels, values []string) {
	index := 0
	for position, value := range values {
		if *target == value {
			index = position
		}
	}
	if segmentedControl(context, key, &index, labels...) {
		*target = values[index]
	}
}

func integerInput(context *native.Context, label string, value *int, minimum, maximum float64) {
	number := float64(*value)
	if numberInput(context, label, &number, minimum, maximum, label).Changed() {
		*value = int(number)
	}
}

func (controller *Controller) scheduleFields(context *native.Context, settings *app.Settings) {
	field(context, "自动备份", func() { native.Switch(context, &settings.ScheduleEnabled).Label("启用自动备份") })
	if !settings.ScheduleEnabled {
		native.Text(context, "开启后会定时检查 PATH，有变化时才保存新快照。").FontSize(detailSize(context)).TextColor(context.Theme().TextMuted)
		return
	}
	field(context, "计划模式", func() {
		choice(context, "schedule-mode", &settings.ScheduleMode, []string{"间隔备份", "每天定时"}, []string{"interval", "daily"})
	})
	if settings.ScheduleMode == "daily" {
		field(context, "本地时间", func() {
			integerInput(context, "每日小时", &settings.DailyHour, 0, 23)
			native.Text(context, "时")
			integerInput(context, "每日分钟", &settings.DailyMinute, 0, 59)
			native.Text(context, "分")
		})
	} else {
		field(context, "备份间隔", func() {
			integerInput(context, "间隔小时", &settings.IntervalHours, 1, 168)
			native.Text(context, "小时（1–168）").TextColor(context.Theme().TextMuted)
		})
	}
}

func (controller *Controller) schedulePage(context *native.Context) {
	native.Scroll(context.Key("schedule-scroll")).Grow(1).MinHeight(0).Children(func() {
		native.Column(context).FillWidth().MaxWidth(820).Gap(8).Children(func() {
			sectionPanel(context, "schedule-editor").Padding(10, 14).Gap(6).Children(func() {
				controller.scheduleFields(context, &controller.draft)
			})
			section(context, "当前计划")
			// 展示已保存的状态，不把尚未提交的开关草稿当作正在运行的计划。
			status, detail := scheduleSummary(controller.view)
			native.Text(context, status).FontSize(sectionSize(context)).FontWeight(600)
			native.Text(context, detail).FontSize(controlTextSize(context)).TextColor(context.Theme().TextMuted)
			divider(context)
			native.Text(context, "隐藏到托盘后继续备份，退出应用后暂停。休眠期间错过的计划，会在恢复运行后检查一次。").FontSize(controlTextSize(context)).TextColor(context.Theme().TextMuted)
			if controller.view.LastPlanResult != "" {
				native.Text(context, "最近结果 · "+controller.view.LastPlanResult).FontSize(controlTextSize(context))
			}
			if secondaryButton(context, "查看执行记录", controlOptions{key: "view-records"}).Clicked() {
				controller.tab = 3
			}
		})
	})
	controller.draftButtons(context, true)
}

func (controller *Controller) settingsPage(context *native.Context) {
	if controller.settingsSection < 1 || controller.settingsSection > 5 {
		controller.settingsSection = 1
	}
	native.Column(context.Key("settings-content")).Grow(1).FillWidth().MinHeight(0).MaxWidth(880).Gap(4).Children(func() {
		selected := 0
		if controller.settingsSection == 4 {
			selected = 2
		} else if controller.settingsSection != 1 {
			selected = 1
		}
		if settingsTabs(context, &selected, "通用", "高级", "更新") {
			controller.settingsSection = []int{1, 5, 4}[selected]
		}
		// 分类各自记住滚动位置；共享草稿不因切换分类或缩放窗口丢失。
		native.Scroll(context.Key(fmt.Sprintf("settings-scroll-%d", controller.settingsSection))).Grow(1).MinHeight(0).Children(func() {
			native.Column(context).FillWidth().Gap(14).Justify(native.Start).Padding(8, 0, 16, 0).Children(func() {
				switch controller.settingsSection {
				case 1:
					controller.appearanceSettings(context)
				case 2:
					controller.advancedBack(context, "网络连接")
					controller.networkSettings(context)
				case 3:
					controller.advancedBack(context, "备份存储")
					controller.storageSettings(context)
				case 4:
					sectionPanel(context, "updates").Padding(10, 14).Gap(6).Children(func() { controller.updateSettings(context) })
				case 5:
					controller.advancedSettings(context)
				}
			})
		})
		controller.draftButtons(context, false)
	})
}

func (controller *Controller) advancedBack(context *native.Context, title string) {
	native.Row(context).Gap(12).Children(func() {
		if secondaryButton(context, "返回高级设置", controlOptions{key: "advanced-back", height: 34}).Clicked() {
			controller.settingsSection = 5
		}
		native.Text(context, title).FontSize(sectionSize(context)).FontWeight(600)
	})
}

func (controller *Controller) advancedSettings(context *native.Context) {
	compactPanel(context, "advanced-options").Padding(0, 0).Dividers(1, context.Theme().Border).Children(func() {
		preference(context, "网络连接", "仅影响 ToolNest 的联网功能", func() {
			if secondaryButton(context, "配置代理", controlOptions{key: "configure-proxy"}).Clicked() {
				controller.settingsSection = 2
			}
		})
		preference(context, "备份存储", "查看快照目录或迁移已有数据", func() {
			if secondaryButton(context, "管理存储", controlOptions{key: "manage-storage"}).Clicked() {
				controller.settingsSection = 3
			}
		})
		preference(context, "自启时隐藏窗口", "登录后安静地在托盘中运行", func() {
			native.Switch(context, &controller.draft.StartInTray).Label("自启直接进入托盘").Disabled(!controller.draft.LaunchAtLogin)
		})
	})
}

func preference(context *native.Context, title, detail string, content func()) {
	width, _ := context.Size()
	row := native.Row(context.Key(title)).MinHeight(46).Gap(20).AlignItems(native.Center)
	if width < 740 {
		row.Column().AlignItems(native.Stretch).Gap(6).Padding(8, 0)
	}
	row.Children(func() {
		label := native.Row(context).Gap(10).MinWidth(0).AlignItems(native.Center)
		if width < 740 {
			label.FillWidth()
		} else {
			label.Grow(1)
		}
		label.Children(func() {
			var icon *native.SVG
			switch title {
			case "主题":
				icon = eyeIcon
			case "字号":
				icon = textSizeIcon
			case "开机自启":
				icon = powerIcon
			case "关闭窗口":
				icon = windowCloseIcon
			}
			if icon != nil {
				native.Row(context).Width(28).Height(28).Radius(9).Justify(native.Center).AlignItems(native.Center).
					Background(context.Theme().Accent.Alpha(0.07)).Children(func() {
					native.Icon(context, icon).Width(16).Height(16).TextColor(context.Theme().Accent)
				})
			}
			native.Column(context).Grow(1).MinWidth(0).Gap(1).Children(func() {
				native.Text(context, title).FontSize(controlTextSize(context))
				if detail != "" {
					native.Text(context, detail).FontSize(detailSize(context)).TextColor(context.Theme().TextMuted).LineHeight(1.35)
				}
			})
		})
		native.Row(context).Gap(8).AlignItems(native.Center).Children(content)
	})
}

func (controller *Controller) appearanceSettings(context *native.Context) {
	compactPanel(context, "appearance").Padding(0, 0).Gap(0).Dividers(1, context.Theme().Border).Children(func() {
		preference(context, "主题", "", func() {
			choice(context, "theme", &controller.draft.Theme, []string{"跟随系统", "浅色", "深色"}, []string{"system", "light", "dark"})
		})
		preference(context, "字号", "", func() {
			choice(context, "text-size", &controller.draft.TextSize, []string{"小", "标准", "大"}, []string{"compact", "standard", "comfortable"})
		})
		preference(context, "开机自启", "登录后在后台运行", func() {
			if native.Switch(context, &controller.draft.LaunchAtLogin).Label("开机自启").Changed() && controller.draft.LaunchAtLogin {
				controller.draft.StartInTray = true
			}
		})
		preference(context, "关闭窗口", "", func() {
			choice(context, "close-behavior", &controller.draft.CloseBehavior, []string{"每次询问", "隐藏托盘", "退出应用"}, []string{"ask", "tray", "exit"})
		})
		if !controller.view.TrayAvailable {
			native.Text(context, "当前托盘不可用，关闭时会再次确认。").FontSize(detailSize(context)).TextColor(context.Theme().Warning).Padding(8, 0)
		}
	})
	// 快捷键和通用设置共享同一套字段行语法，只换分组标题和图标。
	compactPanel(context, "shortcuts").Padding(0, 0).Gap(0).Dividers(1, context.Theme().Border).Children(func() {
		settingsGroup(context, "快捷操作", keyboardIcon)
		field(context, "切换页面", func() {
			native.Text(context, "Ctrl+1–5").FontSize(controlTextSize(context)).Font("Cascadia Mono")
			native.Text(context, "概览、PATH 保护、快照、自动备份、活动记录").FontSize(detailSize(context)).TextColor(context.Theme().TextMuted)
		})
		field(context, "打开设置", func() {
			native.Text(context, "Ctrl+,").FontSize(controlTextSize(context)).Font("Cascadia Mono")
		})
		field(context, "重新读取", func() {
			native.Text(context, "Ctrl+R").FontSize(controlTextSize(context)).Font("Cascadia Mono")
		})
	})
}

func (controller *Controller) generalStatus(context *native.Context) {
	theme := context.Theme()
	count, latest := validSnapshotSummary(controller.view.Snapshots)
	status, frequency := scheduleSummary(controller.view)
	rows := []struct {
		label, value, detail string
		action               func()
	}{
		{"PATH 条目", fmt.Sprintf("用户 %d 项 · 系统 %d 项", len(controller.view.User.Entries), len(controller.view.System.Entries)), scopeSummary(controller.view.User) + " · " + scopeSummary(controller.view.System), func() { controller.page, controller.tab = "path", 0 }},
		{"快照保护", fmt.Sprintf("%d 份可用", count), "手动清理前保留 · 恢复前自动创建保护快照", func() { controller.page, controller.tab = "path", 1 }},
		{"自动备份", status, frequency, func() { controller.page, controller.tab = "path", 2 }},
	}
	if count > 0 {
		rows[1].value += " · 最近 " + displayTime(latest.CreatedAt)
	}
	if controller.view.Settings.ScheduleEnabled {
		rows[2].value += " · " + frequency
	}
	for index, row := range rows {
		value, detail, action := row.value, row.detail, row.action
		native.Row(context.Key(fmt.Sprintf("general-status-%d", index))).MinHeight(52).Gap(9).AlignItems(native.Center).Children(func() {
			native.Icon(context, statusIcon(index)).Width(16).Height(16).TextColor(theme.TextMuted)
			native.Text(context, row.label).FontSize(controlTextSize(context)).Width(88).TextColor(theme.TextMuted)
			native.Column(context).Grow(1).MinWidth(0).Gap(1).Children(func() {
				native.Text(context, value).FontSize(controlTextSize(context)).FontWeight(500).SingleLine().Tooltip(value)
				native.Text(context, detail).FontSize(detailSize(context)).TextColor(theme.TextMuted).SingleLine().Tooltip(detail)
			})
			if compactAction(context, "查看", controlOptions{key: fmt.Sprintf("general-status-open-%d", index), label: "查看" + row.label, icon: chevronRightIcon}).Clicked() {
				action()
			}
		})
		if index < len(rows)-1 {
			divider(context)
		}
	}
}

func statusIcon(index int) *native.SVG {
	switch index {
	case 0:
		return shieldIcon
	case 1:
		return archiveIcon
	default:
		return clockIcon
	}
}

func (controller *Controller) networkSettings(context *native.Context) {
	compactPanel(context, "network").Padding(0, 0).Gap(4).Children(func() {
		field(context, "代理模式", func() {
			choice(context, "proxy-mode", &controller.draft.ProxyMode, []string{"跟随系统", "直连", "自定义"}, []string{"system", "direct", "custom"})
		})
		divider(context)
		if controller.draft.ProxyMode == "custom" {
			field(context, "代理地址", func() {
				textInput(context, "proxy-url", &controller.draft.ProxyURL, "http://127.0.0.1:7890 或 socks5://…", "代理地址")
			})
			field(context, "代理账号", func() {
				textInput(context, "proxy-username", &controller.draft.ProxyUsername, "可选", "代理账号")
			})
			field(context, "代理密码", func() {
				textInput(context, "proxy-password", &controller.draft.ProxyPassword, "留空保留已保存密码", "代理密码").Password()
			})
		} else {
			detail := "沿用系统或环境变量中的代理设置。"
			if controller.draft.ProxyMode == "direct" {
				detail = "ToolNest 直接连接网络，不使用代理。"
			}
			native.Text(context, detail).FontSize(controlTextSize(context)).TextColor(context.Theme().TextMuted).Padding(8, 0)
		}
		field(context, "连接测试", func() {
			if secondaryButton(context, "测试连接", controlOptions{key: "test-proxy", label: "使用草稿测试连接", disabled: controller.busy() || controller.view.ProxyTesting}).Clicked() {
				settings := controller.draft
				if err := validateDraft(settings); err != nil {
					controller.localError = err.Error()
				} else {
					controller.launchBackground("测试更新入口连接", func() { controller.actions.TestProxy(settings) })
				}
			}
			if controller.view.ProxyTesting {
				native.Text(context, "正在测试…").FontSize(controlTextSize(context)).Role(native.RoleStatus)
			}
		})
	})
	native.Text(context, "测试使用当前填写的配置，不会保存。自定义代理失败时不会切换为直连，也不会改变系统代理。").FontSize(detailSize(context)).TextColor(context.Theme().TextMuted)
}

func (controller *Controller) storageSettings(context *native.Context) {
	compactPanel(context, "storage").Padding(0, 0).Gap(4).Children(func() {
		field(context, "快照目录", func() {
			directory := controller.view.Settings.SnapshotDir
			textInput(context, "snapshot-directory", &directory, "", "当前快照目录").ReadOnly(true)
			if secondaryButton(context, "迁移…", controlOptions{key: "migrate-storage", disabled: controller.busy()}).Clicked() {
				controller.chooseSnapshotDirectory()
			}
		})
		native.Row(context).Gap(8).Children(func() {
			if secondaryButton(context, "重新检查目录", controlOptions{key: "recheck-storage", disabled: controller.busy()}).Clicked() {
				controller.launch("重新检查目录", controller.actions.Refresh)
			}
			if secondaryButton(context, "复制快照目录", controlOptions{key: "copy-storage"}).Clicked() {
				controller.actions.Copy(controller.view.Settings.SnapshotDir)
			}
		})
		divider(context)
		preference(context, "用户数据", "查看本机保存的配置与记录", func() {
			if secondaryButton(context, "打开数据目录", controlOptions{key: "open-data"}).Clicked() {
				controller.actions.OpenDataDirectory()
			}
		})
		divider(context)
		if controller.view.LegacyCount > 0 {
			preference(context, "旧版快照", fmt.Sprintf("检测到 %d 份可导入快照", controller.view.LegacyCount), func() {
				if secondaryButton(context, "导入旧版快照…", controlOptions{key: "import-legacy", disabled: controller.busy()}).Clicked() {
					controller.openModal("import")
				}
			})
		}
		if len(controller.view.LegacyEntries) > 0 {
			controller.legacyEntries(context)
		}
	})
	native.Text(context, "目录迁移与旧版导入会单独确认；已有快照不会自动清理。").FontSize(detailSize(context)).TextColor(context.Theme().TextMuted)
}

func (controller *Controller) updateSettings(context *native.Context) {
	update := controller.view.Update
	native.Row(context).Gap(16).Padding(6, 0, 12, 0).Children(func() {
		iconTile(context, nestIcon, 44)
		version := update.Current
		if version == "" {
			version = controller.view.Version
		}
		native.Column(context).Grow(1).Gap(5).Children(func() {
			native.Text(context, "ToolNest").FontSize(pageTitleSize(context)).FontWeight(500)
			native.Text(context, "当前版本 "+version).FontSize(controlTextSize(context)).TextColor(context.Theme().TextMuted).Selectable()
		})
		native.Text(context, updateLabel(update.State)).Role(native.RoleStatus).FontSize(controlTextSize(context)).TextColor(context.Theme().TextMuted)
	})
	divider(context)
	preference(context, "后台检查", "自动检查是否有可用的新版本", func() {
		native.Switch(context, &controller.draft.AutoCheck).Label("自动检查更新")
	})
	divider(context)
	preference(context, "后台下载", "下载完成后由你确认重启", func() {
		native.Switch(context, &controller.draft.AutoDownload).Label("自动下载更新")
	})
	divider(context)
	if update.Available != "" {
		native.Text(context, "可用版本 "+update.Available).FontSize(controlTextSize(context)).FontWeight(600)
	}
	controller.updateProgress(context)
	if update.Error != "" {
		if update.RolledBack {
			native.Text(context, "已回退至 "+controller.view.Version+"，快照和设置已保留。").FontSize(controlTextSize(context)).FontWeight(600)
		}
		native.Text(context, update.Error).FontSize(controlTextSize(context)).TextColor(context.Theme().Danger).Selectable()
		if update.State == "blocked" && len(controller.view.Problems) > 0 {
			native.Text(context, "先处理数据目录或读取问题，再重新尝试更新。").FontSize(controlTextSize(context))
			if secondaryButton(context, "处理数据问题", controlOptions{key: "handle-data-problem"}).Clicked() {
				controller.settingsSection = 3
			}
		}
		if update.RecoveryPath != "" && update.ErrorCode == "recovery" {
			native.Text(context, "恢复文件位置："+update.RecoveryPath).FontSize(detailSize(context)).Selectable()
			if secondaryButton(context, "复制恢复位置", controlOptions{key: "copy-recovery"}).Clicked() {
				controller.actions.Copy(update.RecoveryPath)
			}
		}
	}
	native.Row(context).Gap(8).Wrap().Children(func() {
		working := update.NetworkPhase != "" || update.State == "checking" || update.State == "downloading" || update.State == "verifying"
		if secondaryButton(context, "检查更新", controlOptions{key: "check-update", disabled: controller.pending != "" || working}).Clicked() {
			controller.launchBackground("检查更新", controller.actions.CheckUpdate)
		}
		if working {
			if cancellation, ok := controller.actions.(interface{ CancelDownload() }); ok {
				if secondaryButton(context, "取消检查／下载", controlOptions{key: "cancel-download"}).Clicked() {
					cancellation.CancelDownload()
				}
			}
		}
		if update.Available != "" || update.State == "confirmed" {
			if secondaryButton(context, "版本说明", controlOptions{key: "release-notes"}).Clicked() {
				controller.openModal("release-notes")
			}
		}
		canDownload := update.Available != "" && !working && update.State != "blocked" && update.State != "ready" && update.State != "waiting" && update.State != "waiting-safe-switch" && update.State != "applying" && update.State != "pending-startup-confirmation"
		if canDownload {
			label := "下载更新"
			if update.Error != "" {
				label = "重新下载"
			}
			if primaryButton(context, label, controlOptions{key: "download-update", label: "下载更新", disabled: controller.pending != ""}).Clicked() {
				controller.launchBackground("准备下载更新", controller.actions.DownloadUpdate)
			}
		}
		if update.State == "ready" || update.State == "waiting" || update.State == "waiting-safe-switch" {
			if primaryButton(context, "重启更新…", controlOptions{key: "restart-update", disabled: working || controller.view.PendingExit != ""}).Clicked() {
				controller.openModal("restart")
			}
			if secondaryButton(context, "下次打开时更新", controlOptions{key: "update-next-open"}).Clicked() {
				controller.dismissUpdate()
			}
		}
		if update.State == "blocked" {
			if secondaryButton(context, "重新尝试此版本…", controlOptions{key: "retry-update", disabled: working || len(controller.view.Problems) > 0}).Clicked() {
				controller.openModal("retry-update")
			}
		}
		if update.Error != "" {
			if update.ErrorCode == "network" || update.ErrorCode == "proxy" || update.ErrorCode == "credentials" || update.ErrorCode == "network-config" || update.ErrorCode == "http-407" {
				if secondaryButton(context, "修改公共代理", controlOptions{key: "edit-proxy"}).Clicked() {
					controller.settingsSection = 2
				}
			}
			if secondaryButton(context, "更新错误详情", controlOptions{key: "update-error-detail"}).Clicked() {
				controller.showText("更新错误详情", update.Error+"\n\n"+update.Details)
			}
		}
		if update.SourceURL != "" {
			if actions, ok := controller.actions.(interface{ OpenUpdateSource() }); ok {
				if secondaryButton(context, "打开发布页面", controlOptions{key: "open-update-source"}).Clicked() {
					actions.OpenUpdateSource()
				}
			}
		}
	})
	if update.State == "ready" {
		native.Text(context, "普通退出不会重新打开应用；下次启动会先应用已校验的更新。隐藏到托盘仍继续使用当前版本。").FontSize(detailSize(context)).TextColor(context.Theme().TextMuted)
	}
	if update.NewerVersion != "" {
		native.Text(context, "另有新版 "+update.NewerVersion+"，先完成已准备的更新后再检查。").FontSize(detailSize(context)).TextColor(context.Theme().TextMuted)
	}
	checked := "尚未检查过更新"
	if update.LastCheck != "" {
		checked = "最近检查 · " + update.LastCheck
	}
	native.Text(context, checked).FontSize(detailSize(context)).TextColor(context.Theme().TextMuted)
	if update.PreparedAt != "" {
		native.Text(context, "准备完成 · "+update.PreparedAt).FontSize(detailSize(context)).TextColor(context.Theme().TextMuted)
	}
	// 字体随程序内嵌使用；许可条款要求软件中明确注明，不单独分发字体文件。
	native.Text(context, "界面字体：MiSans（小米）· 许可详情见安装目录 ThirdParty-MiSans.txt").
		FontSize(detailSize(context)).TextColor(context.Theme().TextMuted).Selectable()
}

func (controller *Controller) legacyEntries(context *native.Context) {
	for _, entry := range controller.view.LegacyEntries {
		native.Row(context.Key("legacy-" + entry.ID)).MinHeight(40).Gap(8).Children(func() {
			native.Text(context, entry.Kind+" · "+entry.Description).Grow(1).SingleLine().Tooltip(entry.Description)
			if !entry.Owned {
				native.Text(context, "归属未确认，不改动").FontSize(detailSize(context)).TextColor(context.Theme().TextMuted)
				return
			}
			label := "停用旧入口…"
			kind := "disable-legacy"
			if entry.Disabled {
				label, kind = "撤销停用…", "undo-legacy"
			}
			if secondaryButton(context, label, controlOptions{key: "legacy-" + entry.ID, disabled: controller.busy()}).Clicked() {
				controller.legacyID = entry.ID
				controller.openModal(kind)
			}
		})
	}
}

func (controller *Controller) draftButtons(context *native.Context, scheduleOnly bool) {
	if (!scheduleOnly && controller.draft == controller.base) || (scheduleOnly && sameSchedule(controller.draft, controller.base)) {
		return
	}
	native.Row(context.Key("draft-actions")).MinHeight(44).Gap(8).Justify(native.End).AlignItems(native.Center).Children(func() {
		dirty := controller.draft != controller.base
		if scheduleOnly {
			dirty = !sameSchedule(controller.draft, controller.base)
		}
		if dirty {
			native.Text(context, "有未保存的修改").FontSize(detailSize(context)).TextColor(context.Theme().TextMuted).Grow(1)
		} else if !scheduleOnly {
			native.Text(context, "修改后保存生效").FontSize(detailSize(context)).TextColor(context.Theme().TextMuted).Grow(1)
		} else {
			native.Spacer(context)
		}
		if secondaryButton(context, "取消修改", controlOptions{key: "cancel-draft", disabled: !dirty || controller.busy()}).Clicked() {
			if scheduleOnly {
				copySchedule(&controller.draft, controller.view.Settings)
			} else {
				controller.draft = controller.view.Settings
			}
			controller.base, controller.localError = controller.view.Settings, ""
			controller.submitted = nil
		}
		label := "保存设置"
		if scheduleOnly {
			label = "保存计划"
		}
		if primaryButton(context, label, controlOptions{key: "save-draft", disabled: !dirty || controller.busy()}).Clicked() {
			controller.saveDraft(scheduleOnly)
		}
	})
}

func copySchedule(target *app.Settings, source app.Settings) {
	target.ScheduleEnabled, target.ScheduleMode = source.ScheduleEnabled, source.ScheduleMode
	target.IntervalHours, target.DailyHour, target.DailyMinute = source.IntervalHours, source.DailyHour, source.DailyMinute
}

func (controller *Controller) saveDraft(scheduleOnly bool) {
	settings := controller.draft
	if scheduleOnly {
		settings = controller.view.Settings
		copySchedule(&settings, controller.draft)
	}
	// 目录切换只走复制校验事务，不能被普通设置草稿绕过或写回旧位置。
	settings.SnapshotDir, settings.OnboardingDone = controller.view.Settings.SnapshotDir, controller.view.Settings.OnboardingDone
	if err := validateDraft(settings); err != nil {
		controller.localError = err.Error()
		return
	}
	controller.submitted = &settings
	controller.submittedSchedule = scheduleOnly
	controller.submittedRevision = controller.view.SettingsRevision
	controller.launch("保存设置", func() { controller.actions.SaveSettings(settings) })
}

func (controller *Controller) chooseSnapshotDirectory() {
	if controller.busy() {
		return
	}
	controller.pending = "选择快照目录"
	go func() {
		directory, err := controller.actions.ChooseDirectory()
		controller.events <- func() {
			controller.pending = ""
			if err != nil {
				controller.localError = err.Error()
				return
			}
			if directory == "" || directory == controller.view.Settings.SnapshotDir {
				return
			}
			controller.moveTarget = directory
			controller.openModal("move")
		}
	}()
}

func (controller *Controller) migrationSummary() string {
	valid := 0
	for _, snapshot := range controller.view.Snapshots {
		if snapshot.Error == "" {
			valid++
		}
	}
	return fmt.Sprintf("源目录：%s\n目标目录：%s\n当前可识别快照：%d 份（执行前重新核实）\n\n先复制并校验，全部成功才生效；保留源文件，不覆盖未知文件。失败继续使用原目录。", controller.view.Settings.SnapshotDir, controller.moveTarget, valid)
}
