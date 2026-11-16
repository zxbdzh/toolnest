package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	native "github.com/egoist/mygo/ui"

	"toolnest/internal/app"
)

type testActions struct {
	mu            sync.Mutex
	state         app.View
	previewCalls  []app.Preview
	restored      []app.Preview
	copied        []string
	saved         []app.Settings
	closed        []string
	backups       int
	restarts      int
	updateRetries int
	exitCancels   int
}

func (actions *testActions) View() app.View {
	actions.mu.Lock()
	defer actions.mu.Unlock()
	return actions.state
}
func (actions *testActions) Refresh() {}
func (actions *testActions) Backup() {
	actions.mu.Lock()
	defer actions.mu.Unlock()
	actions.backups++
}
func (actions *testActions) PreviewRestore(id string, user, system bool) (app.Preview, error) {
	actions.mu.Lock()
	defer actions.mu.Unlock()
	preview := app.Preview{Token: "verified-preview", SnapshotID: id, User: user, System: system}
	actions.previewCalls = append(actions.previewCalls, preview)
	return preview, nil
}
func (actions *testActions) Restore(preview app.Preview) {
	actions.mu.Lock()
	defer actions.mu.Unlock()
	actions.restored = append(actions.restored, preview)
}
func (actions *testActions) DeleteSnapshots([]string) {}
func (actions *testActions) SaveSettings(settings app.Settings) {
	actions.mu.Lock()
	defer actions.mu.Unlock()
	actions.saved = append(actions.saved, settings)
}
func (actions *testActions) CompleteOnboarding(app.Settings, bool) {}
func (actions *testActions) ImportLegacy()                         {}
func (actions *testActions) MoveSnapshots(string)                  {}
func (actions *testActions) DisableLegacy(string)                  {}
func (actions *testActions) UndoLegacy(string)                     {}
func (actions *testActions) CheckUpdate()                          {}
func (actions *testActions) DownloadUpdate()                       {}
func (actions *testActions) RestartUpdate()                        { actions.restarts++ }
func (actions *testActions) RetryUpdate()                          { actions.updateRetries++ }
func (actions *testActions) CancelExit()                           { actions.exitCancels++; actions.state.PendingExit = "" }
func (actions *testActions) DismissUpdate(version string) {
	actions.state.Update.DismissedVersion = actions.state.Update.State + ":" + version
}
func (actions *testActions) TestProxy(app.Settings)           {}
func (actions *testActions) Copy(text string)                 { actions.copied = append(actions.copied, text) }
func (actions *testActions) ChooseDirectory() (string, error) { return "", nil }
func (actions *testActions) OpenDataDirectory()               {}
func (actions *testActions) RequestClose(behavior string, remember bool) {
	actions.closed = append(actions.closed, behavior)
}
func (actions *testActions) Quit() {}

func fixture() *testActions {
	return &testActions{state: app.View{
		Version: "test", TrayAvailable: true,
		Settings:  app.Settings{Theme: "system", TextSize: "standard", ProxyMode: "system", CloseBehavior: "ask", OnboardingDone: true, ScheduleMode: "interval", IntervalHours: 6},
		User:      app.Scope{Exists: true, Type: "REG_EXPAND_SZ", Text: "C:\\One;;C:\\One", Entries: []app.Entry{{Index: 1, Text: "C:\\One"}, {Index: 2, Text: ""}, {Index: 3, Text: "C:\\One"}}},
		System:    app.Scope{Exists: true, Type: "REG_SZ", Text: "C:\\Windows", Entries: []app.Entry{{Index: 1, Text: "C:\\Windows"}}},
		Snapshots: []app.Snapshot{{ID: "snapshot-1", CreatedAt: "2026-11-12 12:00", Source: "手动", Summary: "测试快照", User: app.Scope{Exists: true, Type: "REG_SZ", Text: "C:\\Old", Entries: []app.Entry{{Index: 1, Text: "C:\\Old"}}}}},
	}}
}

func TestFilterPreservesOriginalEntries(t *testing.T) {
	entries := fixture().state.User.Entries
	got := filterEntries(entries, "one")
	want := []app.Entry{entries[0], entries[2]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered = %#v, want %#v", got, want)
	}
	if len(filterEntries(entries, "")) != 3 {
		t.Fatal("empty or duplicate entries were removed")
	}
}

func TestDiffRetainsOrderDuplicatesAndEmpty(t *testing.T) {
	before := app.Scope{Exists: true, Type: "REG_SZ", Entries: []app.Entry{{Index: 1, Text: "A"}, {Index: 2, Text: ""}, {Index: 3, Text: "A"}}}
	after := before
	after.Entries = []app.Entry{{Index: 1, Text: "A"}, {Index: 2, Text: "A"}, {Index: 3, Text: ""}}
	changes := displayChanges(before, after, "用户")
	if len(changes) != 2 || changes[0].Before != "（空条目）" || changes[0].After != "A" {
		t.Fatalf("changes = %#v", changes)
	}
}

func TestDiffReportsInvisibleRawByteChanges(t *testing.T) {
	before := app.Scope{Exists: true, Type: "REG_SZ", Text: "A", RawDigest: "before", RawSize: 2}
	after := before
	after.RawDigest = "after"
	after.RawSize = 4
	changes := displayChanges(before, after, "用户")
	if len(changes) != 1 || changes[0].Kind != "原始字节" {
		t.Fatal(changes)
	}
}

func TestNativeLayoutsAndTabs(t *testing.T) {
	for _, width := range []int{1180, 780, 620} {
		controller := New(fixture())
		tester := native.NewTester(controller.render, width, 780)
		if !tester.HasText("PATH 保护") {
			t.Fatalf("missing PATH at width %v", width)
		}
		for _, label := range []string{"快照", "自动备份", "PATH 保护", "应用设置"} {
			if err := tester.Click(label); err != nil {
				t.Fatalf("width %v click %s: %v", width, label, err)
			}
		}
		if !tester.HasText("通用") {
			t.Fatal("settings not rendered")
		}
	}
}

func TestCopyUsesFullUnchangedPath(t *testing.T) {
	actions := fixture()
	controller := New(actions)
	controller.page = "path"
	tester := native.NewTester(controller.render, 1180, 780)
	if err := tester.Click("用户 PATH复制完整值"); err != nil {
		t.Fatal(err)
	}
	if len(actions.copied) != 1 || actions.copied[0] != actions.state.User.Text {
		t.Fatalf("copy = %#v", actions.copied)
	}
}

func TestDraftSurvivesBackgroundRefreshAndCancel(t *testing.T) {
	actions := fixture()
	controller := New(actions)
	controller.synchronize()
	controller.draft.ProxyURL = "http://127.0.0.1:7890"
	actions.state.Status = "备份完成"
	controller.synchronize()
	if controller.draft.ProxyURL == "" {
		t.Fatal("poll erased unsaved draft")
	}
	if actions.state.Settings.ProxyURL != "" {
		t.Fatal("draft changed persisted settings")
	}
	controller.page = "settings"
	tester := native.NewTester(controller.render, 1180, 780)
	if err := tester.Click("取消修改"); err != nil {
		t.Fatal(err)
	}
	if controller.draft != actions.state.Settings {
		t.Fatal("cancel did not reset draft")
	}
	if len(actions.saved) != 0 {
		t.Fatal("cancel submitted settings")
	}
}

func TestRestoreDefaultsAndCancelWriteNothing(t *testing.T) {
	actions := fixture()
	controller := New(actions)
	controller.synchronize()
	controller.beginRestore("snapshot-1")
	deadline := time.Now().Add(time.Second)
	for !controller.previewReady && time.Now().Before(deadline) {
		controller.synchronize()
		time.Sleep(time.Millisecond)
	}
	if !controller.previewReady {
		t.Fatal("preview did not complete")
	}
	if !controller.preview.User || controller.preview.System {
		t.Fatalf("default preview = %#v", controller.preview)
	}
	tester := native.NewTester(controller.render, 1180, 780)
	if err := tester.Click("取消恢复"); err != nil {
		t.Fatal(err)
	}
	actions.mu.Lock()
	defer actions.mu.Unlock()
	if len(actions.restored) != 0 {
		t.Fatal("cancel invoked restore")
	}
}

func TestCancelledPreviewCannotReopenOrAuthorize(t *testing.T) {
	controller := New(fixture())
	controller.synchronize()
	controller.beginRestore("snapshot-1")
	controller.dismissModal()
	controller.synchronize()
	if controller.modalOpen || controller.previewReady {
		t.Fatal("cancelled preview became active")
	}
}

func TestCloseCancelDoesNotPersistChoice(t *testing.T) {
	actions := fixture()
	controller := New(actions)
	controller.RequestClose()
	controller.rememberClose = true
	tester := native.NewTester(controller.render, 1180, 780)
	if err := tester.Click("取消关闭"); err != nil {
		t.Fatal(err)
	}
	if len(actions.closed) != 0 || actions.state.Settings.CloseBehavior != "ask" {
		t.Fatal("cancel closed or persisted preference")
	}
}

func TestProxyValidationDoesNotLeakCredentials(t *testing.T) {
	settings := fixture().state.Settings
	settings.ProxyMode, settings.ProxyURL = "custom", "http://secret:password@127.0.0.1:7890"
	err := validateDraft(settings)
	if err == nil || strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe validation error: %v", err)
	}
	settings.ProxyURL = "socks5://127.0.0.1:7890"
	if err := validateDraft(settings); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsAcknowledgementClearsSubmittedPassword(t *testing.T) {
	actions := fixture()
	controller := New(actions)
	controller.synchronize()
	controller.draft.ProxyMode = "custom"
	controller.draft.ProxyURL = "http://127.0.0.1:7890"
	controller.draft.ProxyUsername = "test-user"
	controller.draft.ProxyPassword = "test-only-secret"
	submitted := controller.draft
	controller.submitted = &submitted
	actions.state.Settings = persistedSettings(submitted)
	actions.state.SettingsRevision++
	actions.state.Error = "系统托盘暂不可用"
	controller.synchronize()
	if controller.submitted != nil || controller.draft.ProxyPassword != "" || controller.draft != controller.base {
		t.Fatal("acknowledgement kept password or left a saved draft dirty")
	}
}

func TestFailedSaveKeepsDraftAndDoesNotAcknowledgeUnchangedPublicFields(t *testing.T) {
	actions := fixture()
	controller := New(actions)
	controller.synchronize()
	controller.draft.ProxyPassword = "test-only-secret"
	submitted := controller.draft
	controller.submitted = &submitted
	actions.state.Error = "系统凭据写入失败"
	controller.synchronize()
	if controller.submitted == nil || controller.draft.ProxyPassword == "" {
		t.Fatal("failed save was treated as successful")
	}
}

func TestDirectoryRefreshKeepsEditableDraftButDoesNotRestoreOldLocation(t *testing.T) {
	actions := fixture()
	controller := New(actions)
	controller.synchronize()
	controller.draft.Theme = "dark"
	actions.state.Settings.SnapshotDir = "F:\\snapshots-after-move"
	controller.synchronize()
	if controller.draft.Theme != "dark" || controller.draft.SnapshotDir != actions.state.Settings.SnapshotDir {
		t.Fatal("directory refresh erased draft or retained old path")
	}
}

func TestScopeSearchRemainsIndependentWhenResizing(t *testing.T) {
	controller := New(fixture())
	controller.page = "path"
	controller.panes[0].query, controller.panes[1].query = "One", "Windows"
	tester := native.NewTester(controller.render, 1180, 780)
	tester.SetSize(620, 520)
	if err := tester.Click("系统 PATH"); err != nil {
		t.Fatal(err)
	}
	tester.SetSize(1180, 780)
	if controller.panes[0].query != "One" || controller.panes[1].query != "Windows" {
		t.Fatal("resizing merged or cleared scope searches")
	}
}

func TestPlanAcknowledgementKeepsUnrelatedDraft(t *testing.T) {
	actions := fixture()
	controller := New(actions)
	controller.synchronize()
	controller.draft.Theme = "dark"
	controller.draft.IntervalHours = 12
	submitted := actions.state.Settings
	copySchedule(&submitted, controller.draft)
	controller.submitted, controller.submittedSchedule = &submitted, true
	actions.state.Settings = submitted
	actions.state.SettingsRevision++
	controller.synchronize()
	if controller.draft.Theme != "dark" || controller.base.Theme != "system" || !sameSchedule(controller.draft, controller.base) {
		t.Fatal("saving plan overwrote unrelated draft or did not acknowledge schedule")
	}
}

func TestRestoreConfirmSubmitsOnlyVerifiedUserScope(t *testing.T) {
	actions := fixture()
	controller := New(actions)
	controller.synchronize()
	controller.beginRestore("snapshot-1")
	deadline := time.Now().Add(time.Second)
	for !controller.previewReady && time.Now().Before(deadline) {
		controller.synchronize()
		time.Sleep(time.Millisecond)
	}
	if !controller.previewReady {
		t.Fatal("preview did not complete")
	}
	tester := native.NewTester(controller.render, 1180, 780)
	if err := tester.Click("确认恢复所选范围"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		actions.mu.Lock()
		if len(actions.restored) > 0 {
			preview := actions.restored[0]
			actions.mu.Unlock()
			if preview.Token != "verified-preview" || !preview.User || preview.System {
				t.Fatalf("restored = %#v", preview)
			}
			return
		}
		actions.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("confirmation did not submit restore")
}

func TestNarrowSnapshotDetailAndDefaultDeleteSelection(t *testing.T) {
	controller := New(fixture())
	controller.page = "path"
	controller.tab = 1
	tester := native.NewTester(controller.render, 620, 520)
	if len(controller.selectedSnapshots()) != 0 {
		t.Fatal("snapshot delete selection was not empty by default")
	}
	if err := tester.Click("查看快照 snapshot-1"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("打开所选快照"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("恢复快照…") {
		t.Fatal("narrow snapshot detail was not displayed")
	}
	if err := tester.Click("返回快照列表"); err != nil {
		t.Fatal(err)
	}
	if controller.snapshotDetailOpen {
		t.Fatal("back did not restore list")
	}
}

func TestSnapshotDeleteSelectionUsesAccessibleCheckbox(t *testing.T) {
	actions := fixture()
	controller := New(actions)
	controller.page = "path"
	controller.tab = 1
	tester := native.NewTester(controller.render, 1180, 780)
	if _, exists := tester.AccessByRole(native.RoleCheckBox, "选择快照 snapshot-1"); !exists {
		t.Fatal("snapshot delete checkbox is missing from the accessibility tree")
	}
	if err := tester.Click("选择快照 snapshot-1"); err != nil {
		t.Fatal(err)
	}
	if len(controller.selectedSnapshots()) != 1 || controller.selectedSnapshots()[0] != "snapshot-1" {
		t.Fatalf("delete checkbox did not select the snapshot: %#v", controller.selectedSnapshots())
	}
	if !tester.HasText("删除选中 (1)") {
		t.Fatal("delete button did not report selected count")
	}
	if err := tester.Click("删除选中 (1)"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("删除 1 份快照？") {
		t.Fatal("delete confirmation was not opened")
	}
	if err := tester.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if len(controller.selectedSnapshots()) != 1 {
		t.Fatal("cancel unexpectedly cleared delete selection")
	}
}

func TestNativeRenderThemesAndScale(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1.25, 1.5, 2} {
			controller := New(fixture())
			controller.page = "path"
			tester := native.NewTester(controller.render, 780, 600)
			tester.SetDark(dark)
			tester.SetScale(scale)
			for _, label := range []string{"刷新 PATH", "立即备份", "应用设置"} {
				bounds, exists := tester.Find(label)
				if !exists || bounds.W <= 0 || bounds.H <= 0 || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > 780.5 || bounds.Y+bounds.H > 600.5 {
					t.Fatalf("dark %v scale %v %q bounds %#v", dark, scale, label, bounds)
				}
			}
		}
	}
}

func TestNarrowDialogsKeepCriticalActionsVisible(t *testing.T) {
	for _, scenario := range []struct{ kind, action string }{
		{"restore", "确认恢复所选范围"}, {"onboarding", "开始使用"}, {"close", "确认关闭"}, {"import", "确认导入"},
	} {
		controller := New(fixture())
		controller.synchronize()
		controller.onboarding = controller.view.Settings
		controller.restoreUser = true
		controller.preview = app.Preview{Token: "verified-preview", SnapshotID: "snapshot-1", User: true}
		controller.openModal(scenario.kind)
		controller.previewReady = true
		tester := native.NewTester(controller.render, 620, 520)
		bounds, exists := tester.Find(scenario.action)
		if !exists || bounds.W <= 0 || bounds.H <= 0 || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > 620.5 || bounds.Y+bounds.H > 520.5 {
			t.Fatalf("dialog %s action %q bounds %#v", scenario.kind, scenario.action, bounds)
		}
	}
}

func TestNativeRenderReview(t *testing.T) {
	directory := os.Getenv("TOOLNEST_UI_REVIEW_DIR")
	if directory == "" {
		t.Skip("set TOOLNEST_UI_REVIEW_DIR to export native headless review images")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, screen := range []struct {
		name          string
		width, height int
		dark          bool
		page          string
		tab           int
		settings      int
	}{
		{"home-light", 1180, 780, false, "home", 0, 0},
		{"home-dark", 1180, 780, true, "home", 0, 0},
		{"home-narrow", 620, 520, false, "home", 0, 0},
		{"current-light", 1180, 780, false, "path", 0, 0},
		{"current-dark", 1180, 780, true, "path", 0, 0},
		{"current-narrow", 620, 520, false, "path", 0, 0},
		{"settings-light", 1180, 780, false, "settings", 0, 1},
		{"settings-dark", 1180, 780, true, "settings", 0, 1},
		{"settings-narrow", 620, 520, false, "settings", 0, 1},
		{"proxy-light", 1180, 780, false, "settings", 0, 2},
		{"proxy-narrow", 620, 520, false, "settings", 0, 2},
		{"updates-light", 1180, 780, false, "settings", 0, 4},
		{"snapshots-dark", 1180, 780, true, "path", 1, 0},
		{"snapshots-light", 1180, 780, false, "path", 1, 0},
		{"snapshots-selected", 1180, 780, false, "path", 1, 0},
		{"snapshot-comparison", 1180, 780, false, "path", 1, 0},
		{"snapshot-comparison-wide", 2048, 1114, false, "path", 1, 0},
		{"schedule", 1180, 780, false, "path", 2, 0},
	} {
		actions := fixture()
		// 预览图补齐几条只读合成数据，展示列表密度；测试夹具不接触用户真实 PATH。
		actions.state.Snapshots = append(actions.state.Snapshots,
			app.Snapshot{ID: "snapshot-2", CreatedAt: "2026-11-14 09:30", Source: "automatic", Summary: "用户 PATH 4 项 · 系统 PATH 2 项",
				User:   app.Scope{Exists: true, Type: "REG_EXPAND_SZ", Text: "C:\\Saved", Entries: []app.Entry{{Index: 1, Text: "C:\\Saved"}}},
				System: app.Scope{Exists: true, Type: "REG_SZ", Text: "C:\\Windows", Entries: []app.Entry{{Index: 1, Text: "C:\\Windows"}}}},
			app.Snapshot{ID: "snapshot-3", CreatedAt: "2026-11-13 18:20", Source: "before-restore", Summary: "用户 PATH 4 项 · 系统 PATH 2 项",
				User: app.Scope{Exists: true, Type: "REG_EXPAND_SZ", Text: "C:\\Old", Entries: []app.Entry{{Index: 1, Text: "C:\\Old"}}}},
		)
		actions.state.Records = []app.Record{
			{At: "2026-11-15 08:42", Operation: "手动备份", Outcome: "success", Message: "快照已保存"},
			{At: "2026-11-14 09:30", Operation: "自动备份", Outcome: "success", Message: "环境无变化，未创建新快照"},
			{At: "2026-11-13 18:20", Operation: "恢复 PATH", Outcome: "success", Message: "用户 PATH 已恢复"},
		}
		// 快照详情底部的执行记录必须有关联 ID，才能作为真实信息而不是夹具占位文案出现。
		actions.state.Records[1].SnapshotID = "snapshot-2"
		// 三页交付截图展示安静的常态；操作反馈仅在状态栏提供入口，不在预览夹具里预置回执。
		controller := New(actions)
		controller.page, controller.tab, controller.settingsSection = screen.page, screen.tab, screen.settings
		if screen.name == "snapshots-selected" {
			controller.snapshotID = "snapshot-2"
		}
		if strings.HasPrefix(screen.name, "snapshot-comparison") {
			controller.snapshotTab = 2
		}
		if screen.settings == 2 {
			controller.synchronize()
			controller.draft.ProxyMode, controller.draft.ProxyURL = "custom", "http://127.0.0.1:7890"
		}
		// 与用户电脑一致地关闭 Windows 透明效果，仍须显示应用自己的磨砂。
		// 深色预览从第一帧就以深色启动，避免截到浅深切换时的起始帧。
		tester := native.NewTesterWith(controller.render, screen.width, screen.height,
			native.TesterOptions{Dark: screen.dark, Preferences: native.Preferences{TextScale: 1, ReduceTransparency: true}})
		filename := filepath.Join(directory, screen.name+".png")
		file, err := os.Create(filename)
		if err != nil {
			t.Fatal(err)
		}
		encodeErr := png.Encode(file, tester.Image())
		closeErr := file.Close()
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		t.Log(filename)
	}
}

// 即使 Windows 不提供系统材质或关闭透明效果，应用完整合成的像素也不能出现透明黑洞。
func TestSystemMaterialFallbackKeepsOpaqueSurfaces(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		vibrancy    bool
		preferences native.Preferences
	}{
		{"no-system-material", false, native.Preferences{}},
		{"reduce-transparency", true, native.Preferences{ReduceTransparency: true}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			controller := New(fixture())
			tester := native.NewTesterWith(controller.render, 1180, 780,
				native.TesterOptions{Preferences: scenario.preferences})
			tester.SetVibrancy(scenario.vibrancy)
			if !tester.HasText("当前环境") || !tester.HasText("最近快照") {
				t.Fatal("opaque fallback lost the home page content")
			}
			image := tester.Image()
			for _, point := range []struct {
				name string
				x, y int
			}{
				{"sidebar", 100, 300}, {"topbar", 400, 20}, {"content", 600, 400},
			} {
				_, _, _, alpha := image.At(point.x, point.y).RGBA()
				if alpha < 0xff00 {
					t.Fatalf("%s fallback left %s transparent", scenario.name, point.name)
				}
			}
		})
	}
}

func TestSettingsCategoriesPreserveDraftAndKeepSaveVisible(t *testing.T) {
	for _, width := range []int{620, 900, 1180} {
		for _, dark := range []bool{false, true} {
			controller := New(fixture())
			controller.ShowSettings()
			tester := native.NewTester(controller.render, width, 780)
			tester.SetDark(dark)
			if err := tester.Click("高级"); err != nil {
				t.Fatal(err)
			}
			if err := tester.Click("配置代理"); err != nil {
				t.Fatal(err)
			}
			if err := tester.Click("自定义"); err != nil {
				t.Fatal(err)
			}
			address, exists := tester.AccessByRole(native.RoleTextField, "代理地址")
			if !exists || address.Disabled {
				t.Fatal("custom proxy address is not editable")
			}
			tester.ClickAt(address.Bounds.X+address.Bounds.W/2, address.Bounds.Y+address.Bounds.H/2)
			tester.Type("http://127.0.0.1:7890")
			tester.SetSize(width, 520)
			for _, label := range []string{"返回高级设置", "管理存储", "更新", "通用", "高级", "配置代理"} {
				if err := tester.Click(label); err != nil {
					t.Fatalf("width %d category %s: %v", width, label, err)
				}
				for _, action := range []string{"保存设置", "取消修改"} {
					bounds, exists := tester.Find(action)
					if !exists || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > float32(width)+0.5 || bounds.Y+bounds.H > 520.5 {
						t.Fatalf("width %d dark %v action %s not visible: %#v", width, dark, action, bounds)
					}
				}
			}
			if controller.draft.ProxyURL != "http://127.0.0.1:7890" || controller.view.Settings.ProxyMode != "system" {
				t.Fatalf("width %d draft mode %q address %q persisted mode %q", width, controller.draft.ProxyMode, controller.draft.ProxyURL, controller.view.Settings.ProxyMode)
			}
		}
	}
}

func TestUpdateActionsFollowPreparationAndStillRequireConfirmation(t *testing.T) {
	for _, scenario := range []struct {
		state, available  string
		download, restart bool
	}{
		{"unchecked", "", false, false},
		{"available", "0.1.1", true, false},
		{"downloading", "0.1.1", false, false},
		{"verifying", "0.1.1", false, false},
		{"ready", "0.1.1", false, true},
		{"waiting-safe-switch", "0.1.1", false, true},
		{"applying", "0.1.1", false, false},
		{"pending-startup-confirmation", "0.1.1", false, false},
	} {
		actions := fixture()
		actions.state.Update = app.Update{State: scenario.state, Available: scenario.available}
		controller := New(actions)
		controller.page, controller.settingsSection = "settings", 4
		tester := native.NewTester(controller.render, 1180, 780)
		_, download := tester.AccessByRole(native.RoleButton, "下载更新")
		_, restart := tester.AccessByRole(native.RoleButton, "重启更新…")
		if download != scenario.download || restart != scenario.restart {
			t.Fatalf("update %s exposes the wrong operation", scenario.state)
		}
		if scenario.restart {
			if err := tester.Click("重启更新…"); err != nil {
				t.Fatal(err)
			}
			if controller.modal != "restart" || !controller.modalOpen {
				t.Fatal("restart action bypassed confirmation")
			}
		}
	}
}
