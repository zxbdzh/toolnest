package ui

import (
	"testing"

	native "github.com/egoist/mygo/ui"
	"toolnest/internal/app"
)

func TestUpdateNoticeDefersWithoutRestartAndKeepsSettingsEntry(t *testing.T) {
	actions := fixture()
	actions.state.Update = app.Update{State: "ready", Current: "0.1.0", Available: "0.1.1", Notes: "# ToolNest 0.1.1\n- 更新操作更清楚。"}
	controller := New(actions)
	tester := native.NewTester(controller.render, 1180, 780)
	if err := tester.Click("查看更新"); err != nil {
		t.Fatal(err)
	}
	if controller.settingsSection != 4 {
		t.Fatal("notice did not open updates")
	}
	if err := tester.Click("下次打开时更新"); err != nil {
		t.Fatal(err)
	}
	if actions.restarts != 0 || actions.state.Update.DismissedVersion != "ready:0.1.1" {
		t.Fatal("deferring restarted or failed to remember the notice")
	}
	tester = native.NewTester(New(actions).render, 1180, 780)
	if _, found := tester.AccessByRole(native.RoleButton, "查看更新"); found {
		t.Fatal("same ready notice returned after reopening the view")
	}
	controller = New(actions)
	controller.ShowUpdates()
	tester = native.NewTester(controller.render, 1180, 780)
	if err := tester.Click("重启更新…"); err != nil {
		t.Fatal(err)
	}
	if actions.restarts != 0 {
		t.Fatal("opening confirmation restarted the application")
	}
	if err := tester.Click("重启更新"); err != nil {
		t.Fatal(err)
	}
	if actions.restarts != 1 {
		t.Fatal("explicit confirmation did not submit restart")
	}
}

func TestUpdateRetryRequiresExplicitConfirmation(t *testing.T) {
	actions := fixture()
	actions.state.Update = app.Update{State: "blocked", Available: "0.1.1", Error: "新版启动失败", ErrorCode: "blocked"}
	controller := New(actions)
	controller.ShowUpdates()
	tester := native.NewTester(controller.render, 1180, 780)
	if _, found := tester.AccessByRole(native.RoleButton, "下载更新"); found {
		t.Fatal("ordinary download could bypass failed-version confirmation")
	}
	if err := tester.Click("重新尝试此版本…"); err != nil {
		t.Fatal(err)
	}
	if actions.updateRetries != 0 {
		t.Fatal("opening the retry dialog authorized the failed version")
	}
	if err := tester.Click("确认重新下载"); err != nil {
		t.Fatal(err)
	}
	if actions.updateRetries != 1 || actions.restarts != 0 {
		t.Fatal("retry did not download independently of restart")
	}
}

func TestWaitingExitHasAccessibleCancellation(t *testing.T) {
	actions := fixture()
	actions.state.PendingExit, actions.state.Busy = "restart", "恢复 PATH"
	tester := native.NewTester(New(actions).render, 620, 520)
	control, found := tester.AccessByRole(native.RoleButton, "取消等待")
	if !found || control.Disabled || control.Bounds.Y+control.Bounds.H > 520 {
		t.Fatal("pending restart cannot be cancelled at the minimum window size")
	}
	if err := tester.Click("取消等待"); err != nil {
		t.Fatal(err)
	}
	if actions.exitCancels != 1 || actions.restarts != 0 {
		t.Fatal("waiting cancellation triggered a restart")
	}
}

func TestUpdateErrorsOfferRelevantRecovery(t *testing.T) {
	for _, scenario := range []struct {
		code  string
		proxy bool
	}{{"http-404", false}, {"http-503", false}, {"signature", false}, {"proxy", true}, {"network", true}} {
		actions := fixture()
		actions.state.Update = app.Update{State: "failed", Error: "更新失败", ErrorCode: scenario.code}
		controller := New(actions)
		controller.ShowUpdates()
		tester := native.NewTester(controller.render, 1180, 780)
		_, found := tester.AccessByRole(native.RoleButton, "修改公共代理")
		if found != scenario.proxy {
			t.Fatalf("wrong recovery action for %s", scenario.code)
		}
	}
}
