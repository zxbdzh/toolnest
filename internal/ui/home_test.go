package ui

import (
	"math"
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
	for _, expected := range []string{"当前环境", "用户 PATH · 3 项", "系统 PATH · 1 项", "最近快照 · 1", "尚未开启"} {
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

func TestHomeRecentSnapshotOpensActualDetail(t *testing.T) {
	controller := New(fixture())
	tester := native.NewTester(controller.render, 1180, 780)
	if err := tester.Click("打开最近快照 snapshot-1"); err != nil {
		t.Fatal(err)
	}
	if controller.snapshotID != "snapshot-1" || controller.tab != 1 || !tester.HasText("恢复快照…") {
		t.Fatal("overview did not open the actual snapshot detail")
	}
	if err := tester.Click("概览"); err != nil {
		t.Fatal(err)
	}
	if controller.page != "home" || controller.snapshotID != "snapshot-1" {
		t.Fatal("navigation erased the selected snapshot")
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
	for _, label := range []string{"查看 PATH", "概览", "应用设置", "概览", "管理备份计划", "概览"} {
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
}

func TestHomeNavigationFitsThemesAndDPI(t *testing.T) {
	for _, dimensions := range [][2]int{{620, 520}, {780, 600}, {1180, 780}, {1440, 900}} {
		for _, dark := range []bool{false, true} {
			for _, scale := range []float32{1, 1.25, 1.5, 2} {
				controller := New(fixture())
				tester := native.NewTester(controller.render, dimensions[0], dimensions[1])
				tester.SetDark(dark)
				tester.SetScale(scale)
				for _, label := range []string{"概览", "PATH 保护", "快照", "自动备份", "活动记录", "应用设置", "备份当前环境", "查看 PATH", "管理备份计划"} {
					bounds, exists := tester.Find(label)
					if !exists || bounds.W <= 0 || bounds.H <= 0 || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > float32(dimensions[0])+0.5 || bounds.Y+bounds.H > float32(dimensions[1])+0.5 {
						t.Fatalf("size=%v dark=%v scale=%v control=%q bounds=%#v", dimensions, dark, scale, label, bounds)
					}
				}
			}
		}
	}
}

func TestNeutralPaletteUsesReadableText(t *testing.T) {
	light := refinedTheme(native.LightTheme())
	dark := refinedTheme(native.DarkTheme())
	if light.Accent != native.Hex("#52697f") || dark.Accent != native.Hex("#718da8") {
		t.Fatal("primary actions must use the restrained slate accent")
	}
	for _, theme := range []*native.Theme{&light, &dark} {
		luminance := func(color native.Color) float64 {
			linear := func(channel uint8) float64 {
				value := float64(channel) / 255
				if value <= 0.04045 {
					return value / 12.92
				}
				return math.Pow((value+0.055)/1.055, 2.4)
			}
			return 0.2126*linear(color.R) + 0.7152*linear(color.G) + 0.0722*linear(color.B)
		}
		foreground, background := luminance(theme.TextMuted), luminance(contentColor(theme))
		contrast := (max(foreground, background) + 0.05) / (min(foreground, background) + 0.05)
		if contrast < 4.5 {
			t.Fatalf("muted text contrast %.2f is below 4.5", contrast)
		}
	}
	if dark.AccentText != native.Hex("#ffffff") || panelColor(&dark) != native.Hex("#232629") {
		t.Fatal("dark theme lost legible primary buttons or opaque panels")
	}
	if light.Font != misansFamily || light.FontSize != 14 {
		t.Fatal("embedded MiSans typography lost its standard size scale")
	}
	compact := refinedTheme(native.LightTheme(), app.Settings{TextSize: "compact"})
	comfortable := refinedTheme(native.LightTheme(), app.Settings{TextSize: "comfortable"})
	if compact.FontSize != 13 || comfortable.FontSize != 16 {
		t.Fatal("text size settings no longer map to three readable scales")
	}
}

// 通用设置只保留可修改项；快照列表下方仍用真实统计信息收口。
func TestDensePagesExposeRealStateAndDirectActions(t *testing.T) {
	actions := fixture()
	controller := New(actions)
	controller.ShowSettings()
	tester := native.NewTester(controller.render, 1180, 780)
	for _, label := range []string{"主题", "字号", "开机自启", "关闭窗口", "跟随系统", "小", "标准", "大"} {
		if !tester.HasText(label) {
			t.Fatalf("settings page missing preference control %q", label)
		}
	}
	for _, removed := range []string{"当前状态", "PATH 列表", "快照列表", "执行记录"} {
		if tester.HasText(removed) {
			t.Fatalf("settings page still shows duplicate dashboard entry %q", removed)
		}
	}
	controller.page, controller.tab = "path", 1
	tester = native.NewTester(controller.render, 1180, 780)
	for _, label := range []string{"查看快照 snapshot-1", "恢复快照…", "份可用"} {
		if !tester.HasText(label) {
			t.Fatalf("snapshot list lost its accessible row action %q", label)
		}
	}
}

// 快照表格按实际行数收口，三份快照仍要保留完整的行操作，统计信息合并到工具栏。
func TestSnapshotListKeepsEveryRowAndFillsRemainder(t *testing.T) {
	actions := fixture()
	actions.state.Snapshots = append(actions.state.Snapshots,
		app.Snapshot{ID: "snapshot-2", CreatedAt: "2026-11-14 09:30", Source: "automatic", Summary: "自动备份"},
		app.Snapshot{ID: "snapshot-3", CreatedAt: "2026-11-13 18:20", Source: "before-restore", Summary: "恢复前保护"},
	)
	controller := New(actions)
	controller.page, controller.tab = "path", 1
	tester := native.NewTester(controller.render, 1180, 780)
	for _, label := range []string{"查看快照 snapshot-1", "查看快照 snapshot-2", "查看快照 snapshot-3", "快照库"} {
		if !tester.HasText(label) {
			t.Fatalf("snapshot list lost %q", label)
		}
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
		if controller.snapshotID != "snapshot-1" {
			t.Fatalf("explicit snapshot action did not open detail at width %d", width)
		}
		if width >= 900 && !tester.HasText("恢复快照…") {
			t.Fatalf("wide snapshot detail was not displayed at width %d", width)
		}
		if width < 900 {
			if err := tester.Click("打开所选快照"); err != nil {
				t.Fatal(err)
			}
			if !tester.HasText("恢复快照…") {
				t.Fatalf("narrow snapshot detail was not displayed at width %d", width)
			}
		}
	}
}

// 连续工作面避免层层卡片；所有分组共享同一套尺寸与字阶。
func TestSharedSurfaceAndTypographyStayConsistent(t *testing.T) {
	light := refinedTheme(native.LightTheme())
	dark := refinedTheme(native.DarkTheme())
	for _, theme := range []*native.Theme{&light, &dark} {
		content, panel := contentColor(theme), panelColor(theme)
		if content.A != 255 || panel.A != 255 {
			t.Fatal("workspace and grouped surfaces must remain opaque")
		}
		if math.Abs(float64(content.R)-float64(panel.R)) > 5 || math.Abs(float64(content.G)-float64(panel.G)) > 5 || math.Abs(float64(content.B)-float64(panel.B)) > 5 {
			t.Fatal("grouped surfaces must stay close to the continuous workspace")
		}
	}
	if light.Spacing != 4 || light.Radius != controlRadius || controlHeight != 32 {
		t.Fatal("shared control metrics drifted apart")
	}
}
