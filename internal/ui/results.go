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

func (controller *Controller) operationNotice(context *native.Context) {
	result := controller.view.Result
	if result.ID == "" || result.ID == controller.dismissedResult {
		return
	}
	native.Row(context.Key("operation-result")).FillWidth().Padding(10, 24).Gap(10).Background(context.Theme().Surface).Children(func() {
		native.Column(context).Grow(1).MinWidth(0).Gap(3).Children(func() {
			native.Text(context, result.Title).FontSize(13).FontWeight(600).Role(native.RoleStatus)
			native.Text(context, result.Message).FontSize(12).TextColor(context.Theme().TextMuted).SingleLine().Tooltip(result.Message)
		})
		if result.SnapshotID != "" && result.Outcome == "success" {
			label := "查看本次快照"
			if result.Operation == "恢复 PATH" {
				label = "查看恢复结果"
			}
			if native.Button(context, label).Height(32).Disabled(controller.busy()).Clicked() {
				if result.Operation == "恢复 PATH" {
					controller.resultShown = result
					controller.openModal("operation-result")
				} else {
					controller.openSnapshot(result.SnapshotID)
				}
			}
		}
		if native.Button(context, "结果详情").Height(32).Clicked() {
			controller.resultShown = result
			controller.openModal("operation-result")
		}
		if native.Button(context, "知道了").Label("收起操作结果").Height(32).Clicked() {
			controller.dismissedResult = result.ID
		}
	})
}

func (controller *Controller) operationResultDialog(context *native.Context) {
	result := controller.resultShown
	native.Text(context, result.Title).FontSize(18).FontWeight(600)
	native.Text(context, result.Message).FontSize(13).Selectable()
	for _, scope := range result.Scopes {
		native.Text(context, scope.Name+"："+scope.Status).FontSize(13)
	}
	if result.Details != "" {
		native.Scroll(context).MaxHeight(170).Children(func() {
			native.Text(context, result.Details).FontSize(12).Selectable()
		})
	}
	native.Row(context).Gap(8).Wrap().Children(func() {
		if result.ProtectionID != "" {
			if native.Button(context, "查看保护快照").Clicked() {
				controller.dismissModal()
				controller.openSnapshot(result.ProtectionID)
			}
			if native.Button(context, "撤回这次恢复…").Disabled(controller.busy()).Clicked() {
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
			if native.Button(context, "重新预览恢复").Disabled(controller.busy()).Clicked() {
				controller.dismissModal()
				controller.beginRestore(result.SnapshotID)
			}
		}
		if result.Operation == "手动备份" && result.Outcome != "success" {
			if native.Button(context, "重试备份").Disabled(controller.busy()).Clicked() {
				controller.dismissModal()
				controller.launch("重试备份", controller.actions.Backup)
			}
		}
		if native.Button(context, "复制结果").Clicked() {
			lines := []string{result.Title, result.Message}
			for _, scope := range result.Scopes {
				lines = append(lines, scope.Name+"："+scope.Status)
			}
			lines = append(lines, result.Details)
			controller.actions.Copy(strings.Join(lines, "\n"))
		}
		if native.PrimaryButton(context, "关闭结果").Clicked() {
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
