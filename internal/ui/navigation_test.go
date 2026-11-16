package ui

import (
	"testing"
	"time"

	native "github.com/egoist/mygo/ui"

	"toolnest/internal/app"
)

func TestKeyboardNavigationKeepsDraftAndRespectsModal(t *testing.T) {
	controller := New(fixture())
	tester := native.NewTester(controller.render, 1180, 780)
	controller.draft.ProxyURL = "http://127.0.0.1:7890"
	for index := range 5 {
		tester.Key(native.Cmd, native.Key1+native.Key(index))
		if index == 0 && controller.page != "home" || index > 0 && (controller.page != "path" || controller.tab != index-1) {
			t.Fatalf("shortcut %d did not navigate", index+1)
		}
	}
	tester.Key(native.Cmd, native.KeyComma)
	if controller.page != "settings" || controller.draft.ProxyURL == "" {
		t.Fatal("settings shortcut discarded the draft")
	}
	controller.openModal("close")
	tester.SetSize(1180, 780)
	tester.Key(native.Cmd, native.Key2)
	if controller.page != "settings" || controller.modal != "close" {
		t.Fatal("navigation bypassed a modal confirmation")
	}
}

func TestSettingsUseProgressiveDisclosure(t *testing.T) {
	controller := New(fixture())
	controller.ShowSettings()
	tester := native.NewTester(controller.render, 620, 520)
	for _, advanced := range []string{"代理模式", "快照目录", "自启时隐藏窗口", "导入旧版快照…"} {
		if tester.HasText(advanced) {
			t.Fatalf("advanced setting %q leaked into general settings", advanced)
		}
	}
	if _, found := tester.Find("保存设置"); found {
		t.Fatal("unchanged settings should not ask to save")
	}
	if err := tester.Click("高级"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("管理存储"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("快照目录") || tester.HasText("导入旧版快照…") {
		t.Fatal("storage must show the directory and omit inapplicable legacy controls")
	}
}

func TestScheduleOffDoesNotRequireFrequencySetup(t *testing.T) {
	controller := New(fixture())
	controller.page, controller.tab = "path", 2
	tester := native.NewTester(controller.render, 620, 520)
	if tester.HasText("备份间隔") || tester.HasText("计划模式") {
		t.Fatal("disabled scheduling exposes unnecessary setup")
	}
	if err := tester.Click("启用自动备份"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("备份间隔") || !tester.HasText("保存计划") || controller.view.Settings.ScheduleEnabled {
		t.Fatal("schedule draft was not disclosed or was applied without saving")
	}
	if err := tester.Click("取消修改"); err != nil {
		t.Fatal(err)
	}
	if controller.draft.ScheduleEnabled || tester.HasText("备份间隔") {
		t.Fatal("cancelling did not restore the saved disabled state")
	}
}

func TestOnboardingDoesNotExposeSettingsBeforeFirstUse(t *testing.T) {
	actions := fixture()
	actions.state.Settings.OnboardingDone = false
	controller := New(actions)
	tester := native.NewTester(controller.render, 620, 520)
	if !tester.HasText("开始使用") || !tester.HasText("先看看") {
		t.Fatal("first run lacks an immediate way to start")
	}
	for _, label := range []string{"计划模式", "备份间隔", "引导开机自启", "引导进入托盘"} {
		if _, exists := tester.Find(label); exists {
			t.Fatalf("first run requires unnecessary configuration: %s", label)
		}
	}
}

func TestSearchMatchesDisplayedDatesAndChineseSources(t *testing.T) {
	actions := fixture()
	timestamp := time.Date(2026, 11, 14, 18, 30, 0, 0, time.Local)
	actions.state.Snapshots[0].CreatedAt = timestamp.Format(time.RFC3339)
	actions.state.Snapshots[0].Source = "before-restore"
	actions.state.Records = []app.Record{{At: timestamp.Format(time.RFC3339), Trigger: "automatic", Outcome: "success", Message: "检查完成"}}
	controller := New(actions)
	controller.page, controller.tab = "path", 1
	tester := native.NewTester(controller.render, 1180, 780)
	for _, query := range []string{"2026-11-14 18:30", "恢复前保护"} {
		controller.snapshotQuery = query
		tester.SetSize(1180, 780)
		if !tester.HasText("2026-11-14 18:30") {
			t.Fatalf("displayed snapshot text is not searchable: %s", query)
		}
	}
	controller.tab = 3
	for _, query := range []string{"2026-11-14 18:30", "自动", "成功"} {
		controller.recordQuery = query
		tester.SetSize(1180, 780)
		if !tester.HasText("检查完成") {
			t.Fatalf("displayed record text is not searchable: %s", query)
		}
	}
	if actions.state.Snapshots[0].CreatedAt != timestamp.Format(time.RFC3339) {
		t.Fatal("display formatting changed the original snapshot timestamp")
	}
}
