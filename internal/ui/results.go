package ui

import (
	"strings"

	"toolnest/internal/app"

	native "github.com/egoist/mygo/ui"
)

func (controller *Controller) openSnapshot(id string) {
	controller.page, controller.tab = "path", 1
	controller.snapshotID, controller.snapshotDetailOpen, controller.snapshotQuery = id, true, ""
	controller.snapshotSelected = -1
	for index, snapshot := range controller.view.Snapshots {
		if snapshot.ID == id {
			controller.snapshotSelected = index
		}
	}
	controller.snapshotPanes[0].selected, controller.snapshotPanes[1].selected = -1, -1
	controller.snapshotLists = [2]native.ListState{}
}

func (controller *Controller) operationResultDialog(context *native.Context) {
	result := controller.resultShown
	native.Text(context, result.Title).FontSize(sectionSize(context)).FontWeight(600)
	native.Text(context, result.Message).FontSize(detailSize(context)).Selectable()
	for _, scope := range result.Scopes {
		native.Text(context, scope.Name+"："+scope.Status).FontSize(detailSize(context))
	}
	if result.Details != "" {
		native.Scroll(context).MaxHeight(170).Children(func() {
			native.Text(context, result.Details).FontSize(detailSize(context)).Selectable()
		})
	}
	native.Row(context).Gap(8).Wrap().Children(func() {
		if result.ProtectionID != "" {
			if secondaryButton(context, "查看保护快照", controlOptions{key: "view-protection"}).Clicked() {
				controller.dismissModal()
				controller.openSnapshot(result.ProtectionID)
			}
			if secondaryButton(context, "撤回这次恢复…", controlOptions{key: "undo-restore", disabled: controller.busy()}).Clicked() {
				user, system := false, false
				for _, scope := range result.Scopes {
					user = user || scope.Name == "用户 PATH"
					system = system || scope.Name == "系统 PATH"
				}
				controller.dismissModal()
				controller.openRestore(result.ProtectionID, user, system)
			}
		}
		if result.Operation == "恢复 PATH" && result.Outcome != "success" && result.SnapshotID != "" {
			if secondaryButton(context, "重新预览恢复", controlOptions{key: "restore-preview", disabled: controller.busy()}).Clicked() {
				controller.dismissModal()
				controller.beginRestore(result.SnapshotID)
			}
		}
		if result.Operation == "手动备份" && result.Outcome != "success" {
			if secondaryButton(context, "重试备份", controlOptions{key: "retry-backup", disabled: controller.busy()}).Clicked() {
				controller.dismissModal()
				controller.launch("重试备份", controller.actions.Backup)
			}
		}
		if secondaryButton(context, "复制结果", controlOptions{key: "copy-result", icon: copyIcon}).Clicked() {
			lines := []string{result.Title, result.Message}
			for _, scope := range result.Scopes {
				lines = append(lines, scope.Name+"："+scope.Status)
			}
			lines = append(lines, result.Details)
			controller.actions.Copy(strings.Join(lines, "\n"))
		}
		if primaryButton(context, "关闭结果", controlOptions{key: "close-result"}).Clicked() {
			controller.dismissModal()
		}
	})
}

func resultFromRecord(record app.Record) (app.OperationResult, bool) {
	if record.Result == nil {
		return app.OperationResult{}, false
	}
	return *record.Result, true
}
