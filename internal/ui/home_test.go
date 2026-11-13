package ui

import (
	"testing"
	"time"

	native "github.com/egoist/mygo/ui"

	"toolnest/internal/app"
)

func TestHomeUsesLiveStateWithoutDemoData(t *testing.T) {
	actions := fixture()
	actions.state.Snapshots = append(actions.state.Snapshots, app.Snapshot{ID: "damaged", CreatedAt: "2099-01-01", Error: "损坏快照"})
	controller := New(actions)
	tester := native.NewTester(controller.render, 1180, 780)
	for _, expected := range []string{"环境、快照与连接，各就其位。", "用户 3 项 · 系统 1 项", "已保留 1 份可用快照", "尚未开启", "跟随系统 · 统一连接设置"} {
		if !tester.HasText(expected) {
			t.Fatalf("missing actual state %q", expected)
		}
	}
	for _, fabricated := range []string{"21 条路径", "20:00", "Demo", "2099-01-01"} {
		if tester.HasText(fabricated) {
			t.Fatalf("unexpected demo or corrupt snapshot state %q", fabricated)
		}
	}
}

func TestHomeToolSearchAndEntryNavigation(t *testing.T) {
	controller := New(fixture())
	tester := native.NewTester(controller.render, 1180, 780)
	if err := tester.Click("搜索工具"); err != nil {
		t.Fatal(err)
	}
	tester.Type("代理")
	if !tester.HasText("公共代理") || tester.HasText("查看与复制环境路径") {
		t.Fatal("tool search did not narrow cards")
	}
	if err := tester.Click("打开公共代理"); err != nil {
		t.Fatal(err)
	}
	if controller.page != "settings" || controller.settingsSection != 2 || !tester.HasText("公共网络代理") {
		t.Fatal("proxy card did not enter the actual proxy settings section")
	}
	if err := tester.Click("工具空间"); err != nil {
		t.Fatal(err)
	}
	if controller.homeQuery != "代理" {
		t.Fatal("navigation erased tool search")
	}
	controller.homeQuery = "不存在的工具"
	tester.SetSize(1180, 780)
	if !tester.HasText("没有匹配的工具") {
		t.Fatal("missing search empty state")
	}
}

func TestHomeBackupSubmitsExistingAction(t *testing.T) {
	actions := fixture()
	controller := New(actions)
	tester := native.NewTester(controller.render, 1180, 780)
	if err := tester.Click("备份当前环境"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		actions.mu.Lock()
		called := actions.backups
		actions.mu.Unlock()
		if called == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("home backup did not submit exactly one real action")
}

func TestHomePreservesPathSearchAndSettingsDraftAcrossNavigation(t *testing.T) {
	controller := New(fixture())
	controller.panes[0].query = "One"
	controller.synchronize()
	controller.draft.ProxyURL = "http://127.0.0.1:7890"
	tester := native.NewTester(controller.render, 1180, 780)
	for _, label := range []string{"查看 PATH", "工具空间", "打开公共代理", "工具空间", "管理备份计划", "工具空间"} {
		if err := tester.Click(label); err != nil {
			t.Fatalf("navigate %q: %v", label, err)
		}
	}
	if controller.panes[0].query != "One" || controller.draft.ProxyURL != "http://127.0.0.1:7890" {
		t.Fatal("home navigation erased unrelated local state")
	}
}

func TestHomeStateSummaries(t *testing.T) {
	count, snapshot := validSnapshotSummary([]app.Snapshot{
		{ID: "old", CreatedAt: "2026-11-11 18:00"},
		{ID: "broken", CreatedAt: "2099-01-01", Error: "unreadable"},
		{ID: "new", CreatedAt: "2026-11-13 09:00"},
	})
	if count != 2 || snapshot.ID != "new" {
		t.Fatalf("snapshot summary = %d %#v", count, snapshot)
	}
	view := app.View{Settings: app.Settings{ScheduleEnabled: true, ScheduleMode: "daily", DailyHour: 9, DailyMinute: 30}, NextRun: "2026-11-14 09:30"}
	status, detail := scheduleSummary(view)
	if status != view.NextRun || detail != "每天 09:30 · 本地时间" {
		t.Fatalf("schedule = %q %q", status, detail)
	}
	view.Settings.ScheduleEnabled = false
	status, _ = scheduleSummary(view)
	if status != "尚未开启" {
		t.Fatal("disabled schedule claimed a pending run")
	}
	if proxySummary("direct") != "直接连接 · 系统代理不变" || proxySummary("custom") != "自定义代理 · 仅用于工具箱" {
		t.Fatal("proxy summary disagrees with settings")
	}
}

func TestHomeNavigationFitsThemesAndDPI(t *testing.T) {
	for _, dimensions := range [][2]int{{620, 520}, {780, 600}, {1180, 780}, {1440, 900}} {
		for _, dark := range []bool{false, true} {
			for _, scale := range []float32{1, 1.25, 1.5, 2} {
				controller := New(fixture())
				tester := native.NewTester(controller.render, dimensions[0], dimensions[1])
				tester.SetDark(dark)
				tester.SetScale(scale)
				for _, label := range []string{"工具空间", "PATH 保护", "快照", "自动备份", "应用设置", "备份当前环境", "查看 PATH", "管理备份计划"} {
					bounds, exists := tester.Find(label)
					if !exists || bounds.W <= 0 || bounds.H <= 0 || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > float32(dimensions[0])+0.5 || bounds.Y+bounds.H > float32(dimensions[1])+0.5 {
						t.Fatalf("size=%v dark=%v scale=%v control=%q bounds=%#v", dimensions, dark, scale, label, bounds)
					}
				}
			}
		}
	}
}

func TestMistPaletteUsesSelectedAccentAndReadableText(t *testing.T) {
	light := refinedTheme(native.LightTheme())
	dark := refinedTheme(native.DarkTheme())
	if light.Accent != native.Hex("#416b8c") || dark.Accent != native.Hex("#416b8c") {
		t.Fatal("production accent differs from the selected mist palette")
	}
	if light.TextMuted != native.Hex("#5c6c78") || dark.TextMuted != native.Hex("#b0c0cc") {
		t.Fatal("muted text no longer follows reviewed contrast values")
	}
	if dark.AccentText != native.Hex("#ffffff") || panelColor(&dark) != native.Hex("#293641") {
		t.Fatal("dark theme lost legible primary buttons or opaque panels")
	}
}

func TestSnapshotHasExplicitAccessibleDetailAction(t *testing.T) {
	for _, width := range []int{620, 1180} {
		controller := New(fixture())
		controller.page, controller.tab = "path", 1
		tester := native.NewTester(controller.render, width, 780)
		if err := tester.Click("查看快照 snapshot-1"); err != nil {
			t.Fatal(err)
		}
		if controller.snapshotID != "snapshot-1" || !tester.HasText("恢复快照…") {
			t.Fatalf("explicit snapshot action did not open detail at width %d", width)
		}
	}
}
