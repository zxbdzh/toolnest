package ui

import (
	"fmt"

	native "github.com/egoist/mygo/ui"

	"toolnest/internal/app"
)

func field(context *native.Context, label string, content func()) {
	width, _ := context.Size()
	labelWidth := float32(126)
	if width < 780 {
		labelWidth = 100
	}
	native.Row(context.Key(label)).MinHeight(48).Gap(12).Children(func() {
		native.Text(context, label).Width(labelWidth).FontSize(14)
		native.Row(context).Grow(1).MinWidth(0).Gap(8).Wrap().Children(content)
	})
}

func section(context *native.Context, title string) {
	native.Text(context, title).FontSize(16).FontWeight(600).Margin(0, 0, 10, 0)
}

func choice(context *native.Context, key string, target *string, labels, values []string) {
	index := 0
	for position, value := range values {
		if *target == value {
			index = position
		}
	}
	if native.Segmented(context.Key(key), &index, labels...).Height(36).FontSize(13).Changed() {
		*target = values[index]
	}
}

func integerInput(context *native.Context, label string, value *int, minimum, maximum float64) {
	number := float64(*value)
	if native.NumberInput(context.Key(label), &number, minimum, maximum, 1).Width(110).Label(label).Changed() {
		*value = int(number)
	}
}

func (controller *Controller) scheduleFields(context *native.Context, settings *app.Settings) {
	field(context, "自动备份", func() { native.Switch(context, &settings.ScheduleEnabled).Label("启用自动备份") })
	field(context, "计划模式", func() {
		choice(context, "schedule-mode", &settings.ScheduleMode, []string{"每 N 小时", "每天定时"}, []string{"interval", "daily"})
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
	width, height := context.Size()
	native.Row(context.Key("schedule-layout")).Height(min(320, max(220, height*0.43))).FillWidth().Gap(18).AlignItems(native.Stretch).Children(func() {
		panel(context.Key("schedule-editor")).Grow(1).MinWidth(0).Padding(18, 22).Gap(8).Children(func() {
			native.Text(context, "备份计划").FontSize(17).FontWeight(600)
			// 草稿和实际计划分开展示，开启开关不冒充已经开始执行。
			native.Scroll(context.Key("schedule-editor-scroll")).Grow(1).MinHeight(0).Children(func() {
				native.Column(context).FillWidth().Gap(2).Children(func() {
					controller.scheduleFields(context, &controller.draft)
					if width < 900 {
						status, detail := scheduleSummary(controller.view)
						native.Text(context, "实际计划 · "+status+"\n"+detail).FontSize(12).TextColor(context.Theme().TextMuted).Padding(8, 0)
					}
				})
			})
			controller.draftButtons(context, true)
		})
		if width >= 900 {
			panel(context.Key("schedule-status")).Width(300).Gap(14).Children(func() {
				native.Row(context).Gap(12).Children(func() {
					iconTile(context, clockIcon, 38)
					native.Text(context, "实际运行状态").FontSize(15).FontWeight(600)
				})
				status, detail := scheduleSummary(controller.view)
				native.Text(context, status).FontSize(18).FontWeight(600).TextColor(mistLink(context.Theme()))
				native.Text(context, detail).FontSize(13).TextColor(context.Theme().TextMuted)
				divider(context)
				native.Text(context, "窗口或托盘运行时执行；环境无变化时只留记录。退出后暂停，错过的计划会合并补跑一次。").FontSize(13).TextColor(context.Theme().TextMuted)
				native.Spacer(context)
				native.Text(context, "失败后等待 5 分钟，最多重试一次。").FontSize(12).TextColor(context.Theme().TextMuted)
			})
		}
	})
	controller.records(context)
}

func (controller *Controller) settingsPage(context *native.Context) {
	width, _ := context.Size()
	if controller.settingsSection < 1 || controller.settingsSection > 4 {
		controller.settingsSection = 1
	}
	wide := width >= 900
	native.Row(context.Key("settings-layout")).Grow(1).MinHeight(0).FillWidth().MaxWidth(1110).AlignSelf(native.Center).AlignItems(native.Stretch).Gap(28).Children(func() {
		if wide {
			controller.settingsNavigation(context)
		}
		native.Column(context.Key("settings-content")).Grow(1).MinWidth(0).MinHeight(0).MaxWidth(820).Gap(16).Children(func() {
			if !wide {
				selected := controller.settingsSection - 1
				if native.Segmented(context.Key("settings-sections"), &selected, "外观与启动", "公共代理", "数据", "更新").Height(36).FontSize(13).Changed() {
					controller.settingsSection = selected + 1
				}
			}
			// 分类各自记住滚动位置；共享草稿不因切换分类或缩放窗口丢失。
			native.Scroll(context.Key(fmt.Sprintf("settings-scroll-%d", controller.settingsSection))).Grow(1).MinHeight(0).Children(func() {
				native.Column(context).FillWidth().Gap(18).Children(func() {
					switch controller.settingsSection {
					case 1:
						settingsHeading(context, settingsIcon, "外观与启动", "选择舒适的外观，设定打开与离开的方式。")
						controller.appearanceSettings(context)
					case 2:
						settingsHeading(context, networkIcon, "公共网络代理", "集中管理 ToolNest 的连接方式，仅作用于本应用。")
						controller.networkSettings(context)
					case 3:
						settingsHeading(context, archiveIcon, "数据与迁移", "让备份有处可寻，迁移前先确认，完成后再切换。")
						controller.storageSettings(context)
					case 4:
						settingsHeading(context, updateIcon, "版本与更新", "后台准备，等你方便的时候再重启。")
						panel(context.Key("updates")).Children(func() { controller.updateSettings(context) })
					}
					controller.statusbar(context)
				})
			})
			controller.draftButtons(context, false)
		})
	})
}

func (controller *Controller) settingsNavigation(context *native.Context) {
	theme := context.Theme()
	native.Column(context.Key("settings-navigation")).Width(184).Padding(8, 0).Gap(6).Children(func() {
		native.Text(context, "偏好设置").FontSize(12).TextColor(theme.TextMuted).Padding(8, 14, 12, 14)
		for index, item := range []struct {
			label string
			icon  *native.SVG
		}{{"外观与启动", settingsIcon}, {"公共代理", networkIcon}, {"数据", archiveIcon}, {"更新", updateIcon}} {
			button := native.Button(context.Key(item.label), "").Label(item.label).FillWidth().Height(44).Padding(0, 14).Gap(12).Radius(10).Border(0, native.Transparent).Background(native.Transparent)
			selected := controller.settingsSection == index+1
			if selected {
				button.Background(panelColor(theme)).Border(1, theme.Border).TextColor(mistLink(theme))
			}
			button.Children(func() {
				native.Icon(context, item.icon).Width(18).Height(18).TextColor(mistLink(theme))
				native.Text(context, item.label).FontSize(14).Grow(1)
			})
			if button.Clicked() {
				controller.settingsSection = index + 1
			}
		}
		native.Spacer(context)
		native.Text(context, "设置按需保存\n切换分类会保留修改").FontSize(12).TextColor(theme.TextMuted).Padding(14)
	})
}

func settingsHeading(context *native.Context, icon *native.SVG, title, detail string) {
	width, _ := context.Size()
	if width < 900 {
		native.Text(context, detail).FontSize(13).TextColor(context.Theme().TextMuted)
		return
	}
	native.Row(context).Gap(14).Padding(4, 0).Children(func() {
		iconTile(context, icon, 44)
		native.Column(context).Grow(1).MinWidth(0).Gap(6).Children(func() {
			native.Text(context, title).FontSize(20).FontWeight(600)
			native.Text(context, detail).FontSize(13).TextColor(context.Theme().TextMuted)
		})
	})
}

func preference(context *native.Context, title, detail string, content func()) {
	native.Row(context.Key(title)).MinHeight(62).Gap(16).Children(func() {
		native.Column(context).Grow(1).MinWidth(0).Gap(5).Children(func() {
			native.Text(context, title).FontSize(14)
			if detail != "" {
				native.Text(context, detail).FontSize(12).TextColor(context.Theme().TextMuted)
			}
		})
		native.Row(context).Gap(8).Children(content)
	})
}

func (controller *Controller) appearanceSettings(context *native.Context) {
	panel(context.Key("appearance")).Gap(6).Children(func() {
		preference(context, "主题", "明暗随系统，或固定为喜欢的外观", func() {
			choice(context, "theme", &controller.draft.Theme, []string{"跟随系统", "浅色", "深色"}, []string{"system", "light", "dark"})
		})
		divider(context)
		preference(context, "开机自启", "登录 Windows 后自动运行 ToolNest", func() {
			if native.Switch(context, &controller.draft.LaunchAtLogin).Label("开机自启").Changed() && controller.draft.LaunchAtLogin {
				controller.draft.StartInTray = true
			}
		})
		divider(context)
		preference(context, "自启进入托盘", "安静地在后台待命", func() {
			native.Switch(context, &controller.draft.StartInTray).Label("自启直接进入托盘").Disabled(!controller.draft.LaunchAtLogin)
		})
		divider(context)
		preference(context, "关闭窗口", "托盘运行时仍会执行备份计划", func() {
			choice(context, "close-behavior", &controller.draft.CloseBehavior, []string{"每次询问", "隐藏托盘", "退出应用"}, []string{"ask", "tray", "exit"})
		})
		if !controller.view.TrayAvailable {
			native.Text(context, "当前托盘不可用，关闭时会再次确认。").FontSize(13).TextColor(context.Theme().Warning)
		}
	})
}

func (controller *Controller) networkSettings(context *native.Context) {
	panel(context.Key("network")).Gap(10).Children(func() {
		field(context, "代理模式", func() {
			choice(context, "proxy-mode", &controller.draft.ProxyMode, []string{"跟随系统", "直连", "自定义"}, []string{"system", "direct", "custom"})
		})
		divider(context)
		if controller.draft.ProxyMode == "custom" {
			field(context, "代理地址", func() {
				native.TextInput(context, &controller.draft.ProxyURL).Placeholder("http://127.0.0.1:7890 或 socks5://…").Label("代理地址").Height(38).FontSize(13).Grow(1).MinWidth(150)
			})
			field(context, "代理账号", func() {
				native.TextInput(context, &controller.draft.ProxyUsername).Placeholder("可选").Label("代理账号").Height(38).FontSize(13).Grow(1)
			})
			field(context, "代理密码", func() {
				native.TextInput(context, &controller.draft.ProxyPassword).Password().Placeholder("留空保留已保存密码").Label("代理密码").Height(38).FontSize(13).Grow(1)
			})
		} else {
			detail := "沿用系统或环境变量中的代理设置。"
			if controller.draft.ProxyMode == "direct" {
				detail = "ToolNest 直接连接网络，不使用代理。"
			}
			native.Text(context, detail).FontSize(13).TextColor(context.Theme().TextMuted).Padding(8, 0)
		}
		field(context, "连接测试", func() {
			if native.Button(context, "测试连接").Label("使用草稿测试连接").Height(36).FontSize(13).Disabled(controller.busy() || controller.view.ProxyTesting).Clicked() {
				settings := controller.draft
				if err := validateDraft(settings); err != nil {
					controller.localError = err.Error()
				} else {
					controller.launchBackground("测试更新入口连接", func() { controller.actions.TestProxy(settings) })
				}
			}
			if controller.view.ProxyTesting {
				native.Text(context, "正在测试…").FontSize(13).Role(native.RoleStatus)
			}
		})
	})
	native.Text(context, "测试使用当前填写的配置，不会保存。自定义代理失败时不会切换为直连，也不会改变系统代理。").FontSize(13).TextColor(context.Theme().TextMuted)
}

func (controller *Controller) storageSettings(context *native.Context) {
	panel(context.Key("storage")).Children(func() {
		field(context, "快照目录", func() {
			directory := controller.view.Settings.SnapshotDir
			native.TextInput(context, &directory).ReadOnly(true).Label("当前快照目录").FontSize(13).Grow(1).MinWidth(150)
			if native.Button(context, "迁移…").Disabled(controller.busy()).Clicked() {
				controller.chooseSnapshotDirectory()
			}
		})
		native.Row(context).Gap(8).Children(func() {
			if native.Button(context, "重新检查目录").Disabled(controller.busy()).Clicked() {
				controller.launch("重新检查目录", controller.actions.Refresh)
			}
			if native.Button(context, "复制快照目录").Clicked() {
				controller.actions.Copy(controller.view.Settings.SnapshotDir)
			}
		})
		divider(context)
		preference(context, "用户数据", "查看本机保存的配置与记录", func() {
			if native.Button(context, "打开数据目录").FontSize(13).Clicked() {
				controller.actions.OpenDataDirectory()
			}
		})
		divider(context)
		preference(context, "旧版快照", fmt.Sprintf("检测到 %d 份可导入快照", controller.view.LegacyCount), func() {
			if native.Button(context, "导入旧版快照…").FontSize(13).Disabled(controller.view.LegacyCount == 0 || controller.busy()).Clicked() {
				controller.openModal("import")
			}
		})
		controller.legacyEntries(context)
	})
	native.Text(context, "目录迁移与旧版导入会单独确认；已有快照不会自动清理。").FontSize(13).TextColor(context.Theme().TextMuted)
}

func (controller *Controller) updateSettings(context *native.Context) {
	update := controller.view.Update
	native.Row(context).Gap(16).Padding(6, 0, 12, 0).Children(func() {
		iconTile(context, nestIcon, 52)
		version := update.Current
		if version == "" {
			version = controller.view.Version
		}
		native.Column(context).Grow(1).Gap(5).Children(func() {
			native.Text(context, "ToolNest").Font("Segoe UI").FontSize(23).FontWeight(600)
			native.Text(context, "当前版本 "+version).FontSize(13).TextColor(context.Theme().TextMuted).Selectable()
		})
		native.Text(context, updateLabel(update.State)).Role(native.RoleStatus).FontSize(13).TextColor(mistLink(context.Theme()))
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
		native.Text(context, "可用版本 "+update.Available).FontSize(14).FontWeight(600)
	}
	controller.updateProgress(context)
	if update.Error != "" {
		if update.RolledBack {
			native.Text(context, "已回退至 "+controller.view.Version+"，快照和设置已保留。").FontSize(13).FontWeight(600)
		}
		native.Text(context, update.Error).FontSize(13).TextColor(context.Theme().Danger).Selectable()
		if update.State == "blocked" && len(controller.view.Problems) > 0 {
			native.Text(context, "先处理数据目录或读取问题，再重新尝试更新。").FontSize(13)
			if native.Button(context, "处理数据问题").Clicked() {
				controller.settingsSection = 3
			}
		}
		if update.RecoveryPath != "" && update.ErrorCode == "recovery" {
			native.Text(context, "恢复文件位置："+update.RecoveryPath).FontSize(12).Selectable()
			if native.Button(context, "复制恢复位置").Clicked() {
				controller.actions.Copy(update.RecoveryPath)
			}
		}
	}
	native.Row(context).Gap(8).Wrap().Children(func() {
		working := update.NetworkPhase != "" || update.State == "checking" || update.State == "downloading" || update.State == "verifying"
		if native.Button(context, "检查更新").Height(36).FontSize(13).Disabled(controller.pending != "" || working).Clicked() {
			controller.launchBackground("检查更新", controller.actions.CheckUpdate)
		}
		if working {
			if cancellation, ok := controller.actions.(interface{ CancelDownload() }); ok {
				if native.Button(context, "取消检查／下载").Clicked() {
					cancellation.CancelDownload()
				}
			}
		}
		if update.Available != "" || update.State == "confirmed" {
			if native.Button(context, "版本说明").Height(36).FontSize(13).Clicked() {
				controller.openModal("release-notes")
			}
		}
		canDownload := update.Available != "" && !working && update.State != "blocked" && update.State != "ready" && update.State != "waiting" && update.State != "waiting-safe-switch" && update.State != "applying" && update.State != "pending-startup-confirmation"
		if canDownload {
			label := "下载更新"
			if update.Error != "" {
				label = "重新下载"
			}
			if native.PrimaryButton(context, label).Label("下载更新").Height(36).FontSize(13).Disabled(controller.pending != "").Clicked() {
				controller.launchBackground("准备下载更新", controller.actions.DownloadUpdate)
			}
		}
		if update.State == "ready" || update.State == "waiting" || update.State == "waiting-safe-switch" {
			if native.PrimaryButton(context, "重启更新…").Height(36).FontSize(13).Disabled(working || controller.view.PendingExit != "").Clicked() {
				controller.openModal("restart")
			}
			if native.Button(context, "下次打开时更新").Height(36).FontSize(13).Clicked() {
				controller.dismissUpdate()
			}
		}
		if update.State == "blocked" {
			if native.Button(context, "重新尝试此版本…").Disabled(working || len(controller.view.Problems) > 0).Clicked() {
				controller.openModal("retry-update")
			}
		}
		if update.Error != "" {
			if update.ErrorCode == "network" || update.ErrorCode == "proxy" || update.ErrorCode == "credentials" || update.ErrorCode == "network-config" || update.ErrorCode == "http-407" {
				if native.Button(context, "修改公共代理").FontSize(13).Clicked() {
					controller.settingsSection = 2
				}
			}
			if native.Button(context, "更新错误详情").FontSize(13).Clicked() {
				controller.showText("更新错误详情", update.Error+"\n\n"+update.Details)
			}
		}
		if update.SourceURL != "" {
			if actions, ok := controller.actions.(interface{ OpenUpdateSource() }); ok {
				if native.Button(context, "打开发布页面").FontSize(13).Clicked() {
					actions.OpenUpdateSource()
				}
			}
		}
	})
	if update.State == "ready" {
		native.Text(context, "普通退出不会重新打开应用；下次启动会先应用已校验的更新。隐藏到托盘仍继续使用当前版本。").FontSize(12).TextColor(context.Theme().TextMuted)
	}
	if update.NewerVersion != "" {
		native.Text(context, "另有新版 "+update.NewerVersion+"，先完成已准备的更新后再检查。").FontSize(12).TextColor(context.Theme().TextMuted)
	}
	checked := "尚未检查过更新"
	if update.LastCheck != "" {
		checked = "最近检查 · " + update.LastCheck
	}
	native.Text(context, checked).FontSize(12).TextColor(context.Theme().TextMuted)
	if update.PreparedAt != "" {
		native.Text(context, "准备完成 · "+update.PreparedAt).FontSize(12).TextColor(context.Theme().TextMuted)
	}
}

func (controller *Controller) legacyEntries(context *native.Context) {
	for _, entry := range controller.view.LegacyEntries {
		native.Row(context.Key("legacy-" + entry.ID)).MinHeight(40).Gap(8).Children(func() {
			native.Text(context, entry.Kind+" · "+entry.Description).Grow(1).SingleLine().Tooltip(entry.Description)
			if !entry.Owned {
				native.Text(context, "归属未确认，不改动").FontSize(12).TextColor(context.Theme().TextMuted)
				return
			}
			label := "停用旧入口…"
			kind := "disable-legacy"
			if entry.Disabled {
				label, kind = "撤销停用…", "undo-legacy"
			}
			if native.Button(context, label).Disabled(controller.busy()).Clicked() {
				controller.legacyID = entry.ID
				controller.openModal(kind)
			}
		})
	}
}

func (controller *Controller) draftButtons(context *native.Context, scheduleOnly bool) {
	native.Row(context.Key("draft-actions")).MinHeight(44).Gap(8).Justify(native.End).Children(func() {
		dirty := controller.draft != controller.base
		if scheduleOnly {
			dirty = !sameSchedule(controller.draft, controller.base)
		}
		if dirty {
			native.Text(context, "有未保存的修改").FontSize(12).TextColor(context.Theme().TextMuted).Grow(1)
		} else if !scheduleOnly {
			native.Text(context, "修改后保存生效").FontSize(12).TextColor(context.Theme().TextMuted).Grow(1)
		} else {
			native.Spacer(context)
		}
		if native.Button(context, "取消修改").Height(36).FontSize(13).Disabled(!dirty || controller.busy()).Clicked() {
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
		if native.PrimaryButton(context, label).Height(36).FontSize(13).Disabled(!dirty || controller.busy()).Clicked() {
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
