package ui

import (
	"testing"

	native "github.com/egoist/mygo/ui"

	"toolnest/internal/app"
)

func TestFeedbackKeepsResultsAndUpdatesReachableBehindAProblem(t *testing.T) {
	for _, width := range []int{620, 1180} {
		actions := fixture()
		actions.state.Problems = []app.Problem{{Title: "快照目录暂不可用", Message: "请检查存储位置", Action: "data"}}
		actions.state.Result = app.OperationResult{ID: "result", Title: "恢复结果", ProtectionID: "protected"}
		actions.state.Update = app.Update{State: "ready", Available: "0.1.8"}
		controller := New(actions)
		tester := native.NewTester(controller.render, width, 520)
		if controller.feedbackKind() != "error" || tester.HasText("恢复结果") || tester.HasText("新版 0.1.8 已准备好") {
			t.Fatal("a problem must take precedence over expanded result and update notices")
		}
		for _, label := range []string{"检查数据目录", "查看原因", "结果详情", "查看更新"} {
			control, exists := tester.AccessByRole(native.RoleButton, label)
			if !exists || control.Bounds.X < 0 || control.Bounds.Y < 0 || control.Bounds.X+control.Bounds.W > float32(width) || control.Bounds.Y+control.Bounds.H > 520 {
				t.Fatalf("width %d lost recovery action %s: %#v", width, label, control)
			}
		}
		if err := tester.Click("结果详情"); err != nil {
			t.Fatal(err)
		}
		if controller.resultShown.ProtectionID != "protected" || controller.modal != "operation-result" {
			t.Fatal("collapsed result lost its protection snapshot")
		}
	}
}

func TestLocalValidationIsNotHiddenByAnExistingDataProblem(t *testing.T) {
	actions := fixture()
	actions.state.Problems = []app.Problem{{Title: "目录不可用", Action: "data"}}
	controller := New(actions)
	controller.localError = "代理地址格式无效"
	tester := native.NewTester(controller.render, 620, 520)
	if !tester.HasText("代理地址格式无效") {
		t.Fatal("local validation was hidden by a persistent problem")
	}
}

func TestResolvedProblemLeavesTheAccessibilityTree(t *testing.T) {
	actions := fixture()
	actions.state.Problems = []app.Problem{{Title: "找不到快照目录", Message: "请检查目录", Action: "data"}}
	actions.state.Result = app.OperationResult{ID: "old-result", Title: "所选 PATH 已恢复"}
	controller := New(actions)
	controller.page, controller.settingsSection = "settings", 3
	tester := native.NewTester(controller.render, 1180, 780)
	if _, found := tester.AccessByRole(native.RoleAlert, "找不到快照目录"); !found {
		t.Fatal("data problem is not announced")
	}
	actions.state.Problems = nil
	tester.SetSize(1180, 780)
	if _, found := tester.AccessByRole(native.RoleAlert, "找不到快照目录"); found || tester.HasText("找不到快照目录") {
		t.Fatal("resolved problem remains in the accessibility tree")
	}
}

func TestConnectionFeedbackSurvivesAnEarlierBackupResult(t *testing.T) {
	actions := fixture()
	actions.state.Result = app.OperationResult{ID: "backup", Operation: "手动备份", Title: "快照已保存"}
	actions.state.Status = "连接成功"
	controller := New(actions)
	tester := native.NewTester(controller.render, 620, 520)
	if !tester.HasText("连接成功") {
		t.Fatal("old backup result hid the new connection test outcome")
	}
	actions.state.Status = "手动备份完成"
	tester.SetSize(620, 520)
	if tester.HasText("手动备份完成") || !tester.HasText("快照已保存") {
		t.Fatal("duplicate backup acknowledgement was not consolidated")
	}
}

func TestResultActionsFitMinimumWindowWithTrayAndUpdateWarnings(t *testing.T) {
	for _, width := range []int{620, 900, 1180, 1440} {
		actions := fixture()
		actions.state.Error = "系统托盘暂不可用"
		actions.state.Update = app.Update{State: "ready", Available: "0.1.8"}
		actions.state.Result = app.OperationResult{ID: "result", Title: "恢复完成", Message: "已读回核对所选范围。需要撤回时，可使用恢复前自动保存的保护快照；已打开的终端需重新打开。", Operation: "恢复 PATH", Outcome: "success", SnapshotID: "snapshot-1"}
		controller := New(actions)
		tester := native.NewTester(controller.render, width, 520)
		for _, label := range []string{"查看恢复结果", "结果详情", "收起操作结果", "错误详情", "查看更新"} {
			control, exists := tester.AccessByRole(native.RoleButton, label)
			if !exists || control.Bounds.X+control.Bounds.W > float32(width)-contentInset(float32(width)-sidebarWidth(float32(width))) || control.Bounds.Y+control.Bounds.H > 520 {
				t.Fatalf("width %d result action %s clipped: %#v", width, label, control)
			}
		}
		if err := tester.Click("收起操作结果"); err != nil {
			t.Fatal(err)
		}
		if controller.feedbackKind() != "error" || !tester.HasText("系统托盘暂不可用") {
			t.Fatal("dismissing result discarded the independent tray warning")
		}
	}
}
