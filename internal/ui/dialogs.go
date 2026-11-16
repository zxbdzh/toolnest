package ui

import (
	"fmt"
	"strings"

	"toolnest/internal/app"

	native "github.com/egoist/mygo/ui"
)

func (controller *Controller) beginRestore(snapshotID string) {
	controller.openRestore(snapshotID, true, false)
}

func (controller *Controller) openRestore(snapshotID string, user, system bool) {
	controller.snapshotID = snapshotID
	controller.restoreUser, controller.restoreSystem = user, system
	controller.openModal("restore")
	controller.loadPreview()
}

func (controller *Controller) loadPreview() {
	controller.previewGeneration++
	generation := controller.previewGeneration
	snapshotID, user, system := controller.snapshotID, controller.restoreUser, controller.restoreSystem
	controller.previewReady, controller.modalError = false, ""
	if !user && !system {
		controller.modalError = "至少选择一个恢复范围。"
		return
	}
	go func() {
		preview, err := controller.actions.PreviewRestore(snapshotID, user, system)
		controller.events <- func() {
			// 范围改变或取消弹窗后，迟到的预览不能重新授权已撤销的操作。
			if controller.modal != "restore" || !controller.modalOpen || generation != controller.previewGeneration {
				return
			}
			if err != nil {
				controller.modalError = err.Error()
				return
			}
			if preview.Token == "" || preview.SnapshotID != snapshotID || preview.User != user || preview.System != system {
				controller.modalError = "恢复预览与所选范围不一致，请重新预览。"
				return
			}
			controller.preview, controller.previewReady = preview, true
		}
	}()
}

func (controller *Controller) dismissModal() {
	controller.previewGeneration++
	controller.modal, controller.modalOpen, controller.previewReady = "", false, false
}

func (controller *Controller) dialogs(context *native.Context) {
	if controller.modal == "" {
		return
	}
	width, height := context.Size()
	kind := controller.modal
	dialog := native.DialogBase(context.Key("dialog-"+kind), &controller.modalOpen, func(backdrop, panel native.Element) {
		backdrop.Background(native.RGBA(0, 0, 0, 0.3))
		floatingSurface(context, panel.Width(min(840, width-32)).MaxHeight(height-32).Padding(14, 18).Gap(8)).
			Transition(motionTransition(context))
		switch kind {
		case "restore":
			controller.restoreDialog(context, min(420, height-270))
		case "onboarding":
			controller.onboardingDialog(context)
		case "operation-result":
			controller.operationResultDialog(context)
		case "close":
			controller.closeDialog(context)
		case "release-notes":
			version := controller.view.Update.Available
			if version == "" {
				version = controller.view.Version
			}
			native.Text(context, "版本说明 · "+version).FontSize(sectionSize(context)).FontWeight(600)
			controller.releaseNotes(context)
			if secondaryButton(context, "关闭说明", controlOptions{key: "close-release-notes"}).Clicked() {
				controller.dismissModal()
			}
		case "text":
			native.Text(context, controller.textTitle).FontSize(sectionSize(context)).Bold()
			native.TextArea(context, &controller.textDetail).ReadOnly(true).Height(min(430, height-180)).FillWidth().Label("完整内容").Font("Cascadia Mono")
			native.Row(context).Gap(8).Justify(native.End).Children(func() {
				if secondaryButton(context, "复制完整内容", controlOptions{key: "copy-text", icon: copyIcon}).Clicked() {
					controller.actions.Copy(controller.textDetail)
				}
				if primaryButton(context, "关闭详情", controlOptions{key: "close-text"}).Clicked() {
					controller.dismissModal()
				}
			})
		default:
			controller.confirmDialog(context, kind)
		}
		if controller.modalError != "" {
			native.Text(context, controller.modalError).Role(native.RoleAlert).TextColor(context.Theme().Danger).Selectable()
		}
	})
	if dialog.Dismissed() {
		if kind == "onboarding" {
			// 引导关闭仅本次不再打扰；跳过按钮才持久化“已引导”。
			controller.onboarding = controller.view.Settings
		}
		controller.dismissModal()
	}
}

func (controller *Controller) restoreDialog(context *native.Context, tableHeight float32) {
	native.Text(context, "恢复 PATH 快照").FontSize(sectionSize(context)).Bold()
	native.Text(context, "方向：当前 PATH → 所选快照。默认仅恢复发起用户；系统范围需单独授权。").FontSize(detailSize(context)).TextColor(context.Theme().TextMuted)
	native.Text(context, "快照："+controller.snapshotID).FontSize(detailSize(context)).SingleLine()
	native.Row(context).Gap(20).Children(func() {
		changed := native.Checkbox(context, &controller.restoreUser, "用户 PATH").Disabled(controller.busy()).Changed()
		changed = native.Checkbox(context, &controller.restoreSystem, "系统 PATH（请求管理员授权）").Disabled(controller.busy()).Changed() || changed
		if changed {
			controller.loadPreview()
		}
	})
	if !controller.previewReady && controller.modalError == "" {
		native.Text(context, "正在读取并校验恢复预览…").Role(native.RoleStatus)
	}
	native.Column(context.Key("restore-changes")).Height(max(100, tableHeight)).Children(func() {
		if controller.previewReady {
			controller.changesView(context, controller.preview.Changes)
		}
	})
	native.Text(context, "确认后先核对预览是否过期、获取所需权限并创建保护快照；任何前置失败都不开始写入。取消授权取消整次恢复。").FontSize(detailSize(context)).TextColor(context.Theme().TextMuted)
	native.Row(context).Gap(8).Justify(native.End).Children(func() {
		if secondaryButton(context, "取消恢复", controlOptions{key: "cancel-restore"}).Clicked() {
			controller.dismissModal()
		}
		if secondaryButton(context, "重新预览", controlOptions{key: "reload-restore-preview", disabled: controller.busy() || (!controller.restoreUser && !controller.restoreSystem)}).Clicked() {
			controller.loadPreview()
		}
		if primaryButton(context, "确认恢复所选范围", controlOptions{key: "confirm-restore", disabled: controller.busy() || !controller.previewReady || (!controller.restoreUser && !controller.restoreSystem)}).Clicked() {
			preview := controller.preview
			controller.dismissModal()
			controller.launch("提交恢复请求", func() { controller.actions.Restore(preview) })
		}
	})
}

func (controller *Controller) closeDialog(context *native.Context) {
	native.Text(context, "关闭 ToolNest").FontSize(sectionSize(context)).Bold()
	if controller.busy() {
		native.Text(context, "正在执行操作。可取消关闭继续等待；业务层未允许安全退出时保留窗口，不会中途强杀写入。").TextColor(context.Theme().Warning)
	}
	native.RadioGroup(context, func() {
		native.Radio(context, &controller.closeChoice, "tray", "隐藏到托盘，继续应用内计划").Disabled(!controller.view.TrayAvailable)
		native.Radio(context, &controller.closeChoice, "exit", "退出应用，停止应用内计划")
	})
	if !controller.view.TrayAvailable {
		native.Text(context, "托盘当前不可用，只能保持窗口或安全退出。").FontSize(detailSize(context)).TextColor(context.Theme().Warning)
	}
	native.Checkbox(context, &controller.rememberClose, "记住这次选择（可在设置中修改）")
	native.Row(context).Gap(8).Justify(native.End).Children(func() {
		if secondaryButton(context, "取消关闭", controlOptions{key: "cancel-close"}).Clicked() {
			controller.dismissModal()
		}
		if primaryButton(context, "确认关闭", controlOptions{key: "confirm-close", disabled: controller.closeChoice == "tray" && !controller.view.TrayAvailable}).Clicked() {
			behavior, remember := controller.closeChoice, controller.rememberClose
			controller.dismissModal()
			controller.actions.RequestClose(behavior, remember)
		}
	})
}

func (controller *Controller) onboardingDialog(context *native.Context) {
	native.Text(context, "开始使用 ToolNest").FontSize(sectionSize(context)).Bold()
	native.Text(context, "先为当前 PATH 留一份备份。以后安装新工具，可以随时查看变化，或恢复到之前的状态。").FontSize(detailSize(context)).TextColor(context.Theme().TextMuted)
	_, supportsInitialBackup := controller.actions.(interface{ CompleteOnboardingWithBackup(app.Settings, bool) })
	native.Checkbox(context, &controller.initialBackup, "开始时创建首份快照（不修改 PATH）").Disabled(!supportsInitialBackup)
	native.Text(context, "自动备份和开机自启可稍后开启。").FontSize(detailSize(context)).TextColor(context.Theme().TextMuted)
	if controller.view.LegacyCount > 0 {
		native.Textf(context, "发现 %d 份旧快照；开始后可在设置中确认导入，不会自动启用旧计划。", controller.view.LegacyCount).FontSize(detailSize(context))
	}
	native.Row(context).Gap(8).Justify(native.End).Children(func() {
		if secondaryButton(context, "先看看", controlOptions{key: "onboarding-skip", disabled: controller.busy()}).Clicked() {
			settings := controller.view.Settings
			controller.dismissModal()
			controller.launch("跳过首次引导", func() { controller.actions.CompleteOnboarding(settings, true) })
		}
		if primaryButton(context, "开始使用", controlOptions{key: "onboarding-start", disabled: controller.busy()}).Clicked() {
			settings := controller.onboarding
			if err := validateDraft(settings); err != nil {
				controller.modalError = err.Error()
				return
			}
			controller.dismissModal()
			if initial, ok := controller.actions.(interface{ CompleteOnboardingWithBackup(app.Settings, bool) }); ok {
				backup := controller.initialBackup
				controller.launch("保存首次设置", func() { initial.CompleteOnboardingWithBackup(settings, backup) })
			} else {
				controller.launch("保存首次设置", func() { controller.actions.CompleteOnboarding(settings, false) })
			}
		}
	})
}

func (controller *Controller) confirmDialog(context *native.Context, kind string) {
	title, body, confirm := "", "", "确认"
	switch kind {
	case "delete":
		title, confirm = fmt.Sprintf("删除 %d 份快照？", len(controller.deleteIDs)), "确认删除"
		var descriptions []string
		for _, id := range controller.deleteIDs {
			for _, snapshot := range controller.view.Snapshots {
				if snapshot.ID == id {
					descriptions = append(descriptions, snapshot.CreatedAt+" · "+snapshot.Source+" · "+id)
				}
			}
		}
		body = strings.Join(descriptions, "\n") + "\n\n只删除选中的快照，不修改当前 PATH。此操作不能撤销；需要时先复制或备份历史文件。"
	case "move":
		title, body, confirm = "迁移快照目录？", controller.migrationSummary(), "复制校验并切换"
	case "import":
		title, confirm = "导入旧版快照？", "确认导入"
		body = fmt.Sprintf("已发现 %d 份旧快照。逐份校验并复制，保留原文件和原始时间；相同内容跳过，冲突和损坏单独报告。不会启用自启或旧计划。", controller.view.LegacyCount)
	case "disable-legacy":
		title, body, confirm = "停用已确认的旧版入口？", "仅处理业务层已核实归属的入口："+controller.legacyID+"\n保留程序、快照及撤销信息，不停止现有进程。执行前再次核对归属。", "确认停用"
	case "undo-legacy":
		title, body, confirm = "撤销旧版入口停用？", "入口："+controller.legacyID+"\n业务层将再次核对现状，不覆盖之后被其他程序修改的配置。", "确认撤销停用"
	case "restart":
		title, body, confirm = "重启以应用更新？", "仅在签名校验及安全切换条件满足后重新启动。关键写入未完成时不重启；选择稍后继续当前版本，下次安全启动再应用。", "重启更新"
		if controller.draft != controller.base {
			body = "还有未保存的设置。请先保存或取消修改，再重启更新，以免丢失刚刚填写的内容。"
		}
	case "retry-update":
		title, body, confirm = "重新尝试此版本？", "这个版本曾启动失败。确认后重新下载并验证，完成前继续使用当前版本；不会立即重启。", "确认重新下载"
	}
	native.Text(context, title).FontSize(sectionSize(context)).Bold()
	native.Scroll(context).MaxHeight(280).Children(func() { native.Text(context, body).Selectable() })
	native.Row(context).Gap(8).Justify(native.End).Children(func() {
		cancel := "取消"
		if kind == "restart" {
			cancel = "稍后更新"
		}
		if secondaryButton(context, cancel, controlOptions{key: "confirm-cancel"}).Clicked() {
			if kind == "restart" {
				controller.dismissUpdate()
			}
			controller.dismissModal()
		}
		if primaryButton(context, confirm, controlOptions{key: "confirm-action", disabled: controller.busy() && kind != "restart" || kind == "restart" && controller.draft != controller.base}).Clicked() {
			controller.dismissModal()
			switch kind {
			case "delete":
				ids := append([]string(nil), controller.deleteIDs...)
				controller.launch("删除所选快照", func() { controller.actions.DeleteSnapshots(ids) })
			case "move":
				directory := controller.moveTarget
				controller.launch("复制并校验快照", func() { controller.actions.MoveSnapshots(directory) })
			case "import":
				controller.launch("导入旧版快照", controller.actions.ImportLegacy)
			case "disable-legacy":
				id := controller.legacyID
				controller.launch("停用旧版入口", func() { controller.actions.DisableLegacy(id) })
			case "undo-legacy":
				id := controller.legacyID
				controller.launch("撤销停用", func() { controller.actions.UndoLegacy(id) })
			case "restart":
				controller.actions.RestartUpdate()
			case "retry-update":
				if actions, ok := controller.actions.(interface{ RetryUpdate() }); ok {
					actions.RetryUpdate()
				}
			}
		}
	})
}
