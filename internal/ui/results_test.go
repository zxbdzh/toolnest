package ui

import (
	"testing"

	native "github.com/egoist/mygo/ui"
	"toolnest/internal/app"
)

func TestBackupResultOpensItsSnapshotAndUndoStillRequiresPreview(t *testing.T) {
	actions := fixture()
	actions.state.Result = app.OperationResult{ID: "result-1", Operation: "手动备份", Outcome: "success", Title: "快照已保存", SnapshotID: "snapshot-1"}
	controller := New(actions)
	tester := native.NewTester(controller.render, 1180, 780)
	if err := tester.Click("查看本次快照"); err != nil {
		t.Fatal(err)
	}
	if controller.page != "path" || controller.tab != 1 || controller.snapshotID != "snapshot-1" || len(actions.restored) != 0 {
		t.Fatal("viewing backup did not navigate without mutation")
	}
	controller.resultShown = app.OperationResult{Operation: "恢复 PATH", Outcome: "success", ProtectionID: "protection", Scopes: []app.ScopeResult{{Name: "用户 PATH"}}}
	controller.openModal("operation-result")
	tester = native.NewTester(controller.render, 1180, 780)
	if err := tester.Click("撤回这次恢复…"); err != nil {
		t.Fatal(err)
	}
	if controller.modal != "restore" || controller.snapshotID != "protection" || !controller.restoreUser || controller.restoreSystem || len(actions.restored) != 0 {
		t.Fatal("undo bypassed preview or expanded recovery scope")
	}
}

func TestDataProblemBlocksRetryAndOffersDataSettings(t *testing.T) {
	actions := fixture()
	actions.state.Problems = []app.Problem{{Code: "snapshot-directory-missing", Title: "找不到快照目录", Action: "data"}}
	actions.state.Update = app.Update{State: "blocked", Available: "0.1.6", Error: "新版启动失败", RolledBack: true}
	controller := New(actions)
	controller.ShowUpdates()
	tester := native.NewTester(controller.render, 1180, 1100)
	button, ok := tester.AccessByRole(native.RoleButton, "重新尝试此版本…")
	if !ok || !button.Disabled {
		t.Fatal("retry enabled while data initialization remains broken")
	}
	if err := tester.Click("处理数据问题"); err != nil {
		t.Fatal(err)
	}
	if controller.settingsSection != 3 {
		t.Fatal("recovery action did not open data settings")
	}
}
