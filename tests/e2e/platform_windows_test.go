//go:build windows

package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"toolnest/internal/app"
	"toolnest/internal/core"
	"toolnest/internal/platform"
	"toolnest/internal/platform/lifecycle"
)

type rawRegistryValue struct {
	Exists bool
	Type   uint32
	Digest [32]byte
}

func readGuardValue(t *testing.T, root registry.Key, path, name string) rawRegistryValue {
	t.Helper()
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return rawRegistryValue{}
	}
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	size, kind, err := key.GetValue(name, nil)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return rawRegistryValue{}
	}
	if err != nil && !errors.Is(err, syscall.ERROR_MORE_DATA) {
		t.Fatal(err)
	}
	data := make([]byte, size)
	_, kind, err = key.GetValue(name, data)
	if err != nil {
		t.Fatal(err)
	}
	return rawRegistryValue{true, kind, sha256.Sum256(data)}
}
func hostGuard(t *testing.T) {
	t.Helper()
	type key struct {
		root       registry.Key
		path, name string
	}
	keys := []key{{registry.CURRENT_USER, `Environment`, "Path"}, {registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, "Path"}, {registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, "ToolNest"}, {registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall\ToolNest`, "InstallLocation"}}
	before := make([]rawRegistryValue, len(keys))
	for index, item := range keys {
		before[index] = readGuardValue(t, item.root, item.path, item.name)
	}
	t.Cleanup(func() {
		for index, item := range keys {
			if readGuardValue(t, item.root, item.path, item.name) != before[index] {
				t.Error("HOST SAFETY VIOLATION: production registry changed")
			}
		}
	})
}

type nativeName struct {
	Length, MaximumLength uint16
	Buffer                *uint16
}

func seedTestValue(t *testing.T, id string, scope core.Scope, value core.Value) {
	t.Helper()
	if len(id) != 32 {
		t.Fatal("unsafe test id")
	}
	path := `Software\ToolNest\Tests\` + id + `\` + string(scope)
	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS|registry.WOW64_64KEY)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	if !value.Exists {
		err = key.DeleteValue("Path")
		if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
			err = nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	// 标准字符串 API 会补终止符；仅向固定测试键写原始字节以验证异常 UTF-16 往返。
	name, _ := windows.UTF16PtrFromString("Path")
	native := nativeName{8, 10, name}
	var pointer *byte
	if len(value.Bytes) > 0 {
		pointer = &value.Bytes[0]
	}
	status, _, _ := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtSetValueKey").Call(uintptr(key), uintptr(unsafe.Pointer(&native)), 0, uintptr(value.Type), uintptr(unsafe.Pointer(pointer)), uintptr(len(value.Bytes)))
	runtime.KeepAlive(value.Bytes)
	runtime.KeepAlive(name)
	if int32(status) < 0 {
		t.Fatal(windows.NTStatus(status).Errno())
	}
}

func TestAT01AT06WindowsRawRestoreOnlyDedicatedKeys(t *testing.T) {
	hostGuard(t)
	id := core.NewID()
	adapter, err := platform.NewRegistry(platform.Options{TestRoot: id})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, scope := range []core.Scope{core.User, core.System} {
			err := registry.DeleteKey(registry.CURRENT_USER, `Software\ToolNest\Tests\`+id+`\`+string(scope))
			if err != nil {
				t.Error(err)
			}
		}
		if err := registry.DeleteKey(registry.CURRENT_USER, `Software\ToolNest\Tests\`+id); err != nil {
			t.Error(err)
		}
	}()
	ctx := context.Background()
	initial, err := adapter.Read(ctx)
	if err != nil || initial.User.Exists || initial.System.Exists {
		t.Fatal("missing test key read failed")
	}
	if key, err := registry.OpenKey(registry.CURRENT_USER, `Software\ToolNest\Tests\`+id, registry.READ); err == nil {
		key.Close()
		t.Fatal("read created key")
	}
	seedTestValue(t, id, core.System, raw("synthetic-system", 2))
	service := &core.Service{Registry: adapter, Store: core.Store{Directory: t.TempDir()}, Now: time.Now}
	for _, value := range []core.Value{raw(`A;;A;%SYNTHETIC%; `, 2), {Exists: true, Type: 1, Bytes: []byte{0, 0xd8, 1}}, {Exists: true, Type: 1, Bytes: []byte{}}, raw("", 1), {}} {
		seedTestValue(t, id, core.User, value)
		snapshot, _, err := service.Backup(ctx, "manual")
		if err != nil {
			t.Fatal(err)
		}
		if !snapshot.User.Equal(value) {
			t.Fatal("read normalized bytes")
		}
		seedTestValue(t, id, core.User, raw("replacement", 1))
		plan, err := service.Preview(ctx, snapshot.ID, []core.Scope{core.User})
		if err != nil {
			t.Fatal(err)
		}
		if err = adapter.ConfirmRestore(plan); err != nil {
			t.Fatal(err)
		}
		result := service.Restore(ctx, plan)
		if result.Error != nil || result.Outcome != "success" || result.ProtectionID == "" {
			t.Fatalf("restore %+v", result)
		}
		restored, err := adapter.Read(ctx)
		if err != nil || !restored.User.Equal(value) || !restored.System.Equal(snapshot.System) {
			t.Fatalf("raw restoration mismatch: %v", err)
		}
	}
}

func waitController(t *testing.T, controller *app.Controller, ready func(app.View) bool) app.View {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		view := controller.View()
		if view.Busy == "" && ready(view) {
			return view
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("controller did not settle: %+v", controller.View())
	return app.View{}
}

func TestAT10AT12AT14ApplicationActionsPersistAndMove(t *testing.T) {
	hostGuard(t)
	root := t.TempDir()
	registry := &syntheticRegistry{state: core.State{User: raw("synthetic-user", 1), System: raw("synthetic-system", 2)}}
	controller, err := app.New(app.Options{Root: root, Version: "0.1.0", Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	initial := controller.View()
	if initial.Settings.ScheduleEnabled || initial.Settings.LaunchAtLogin || initial.Settings.IntervalHours != 6 {
		t.Fatal("unsafe initial settings")
	}
	controller.CompleteOnboarding(initial.Settings, true)
	waitController(t, controller, func(view app.View) bool { return view.Settings.OnboardingDone })
	controller.Backup()
	view := waitController(t, controller, func(view app.View) bool { return len(view.Snapshots) == 1 })
	settings := view.Settings
	settings.Theme = "dark"
	controller.SaveSettings(settings)
	waitController(t, controller, func(view app.View) bool { return view.Settings.Theme == "dark" })
	blockedDirectory := filepath.Join(t.TempDir(), "unknown-target")
	if err = os.Mkdir(blockedDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	unknownFile := filepath.Join(blockedDirectory, view.Snapshots[0].ID+".json")
	if err = os.WriteFile(unknownFile, []byte("unknown target data"), 0600); err != nil {
		t.Fatal(err)
	}
	previousRecords := len(controller.View().Records)
	controller.MoveSnapshots(blockedDirectory)
	failedMove := waitController(t, controller, func(view app.View) bool { return len(view.Records) > previousRecords })
	if failedMove.Settings.SnapshotDir != initial.Settings.SnapshotDir {
		t.Fatal("failed move committed new directory")
	}
	if content, _ := os.ReadFile(unknownFile); string(content) != "unknown target data" {
		t.Fatal("failed move overwrote unknown destination")
	}
	destination := filepath.Join(t.TempDir(), "moved-snapshots")
	controller.MoveSnapshots(destination)
	waitController(t, controller, func(view app.View) bool { return view.Settings.SnapshotDir == destination })
	if _, err = os.Stat(filepath.Join(initial.Settings.SnapshotDir, view.Snapshots[0].ID+".json")); err != nil {
		t.Fatal("move deleted source")
	}
	settingsBytes, err := os.ReadFile(filepath.Join(root, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settingsDocument map[string]any
	if err = json.Unmarshal(settingsBytes, &settingsDocument); err != nil {
		t.Fatal(err)
	}
	reopened, err := app.New(app.Options{Root: root, Version: "0.1.1", Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	actual := reopened.View()
	if !reflect.DeepEqual(actual.Settings, controller.View().Settings) || len(actual.Snapshots) != 1 {
		t.Fatal("settings/snapshot continuity lost")
	}
	t.Cleanup(reopened.Quit)
	t.Cleanup(controller.Quit)
	release, err := controller.TryAcquire("acceptance-write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = controller.TryAcquire("update-apply"); err == nil {
		t.Fatal("conflicting gate acquired")
	}
	release()
}

type absentTasks struct{}

func (absentTasks) ReadTask(context.Context, string) (lifecycle.TaskValue, error) {
	return lifecycle.TaskValue{}, nil
}
func (absentTasks) SetTaskEnabled(context.Context, string, lifecycle.TaskValue, bool) (lifecycle.TaskValue, error) {
	return lifecycle.TaskValue{}, errors.New("acceptance must not touch Task Scheduler")
}

func TestAT15DedicatedRunOwnershipDisableUndoAndConflict(t *testing.T) {
	hostGuard(t)
	ctx := context.Background()
	id := core.NewID()
	path := `Software\ToolNest\Tests\` + id + `\Run`
	store, err := lifecycle.NewIsolatedWindowsRunStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, key := range []string{path, `Software\ToolNest\Tests\` + id} {
			if err := registry.DeleteKey(registry.CURRENT_USER, key); err != nil {
				t.Error(err)
			}
		}
	}()
	journal, err := lifecycle.NewFileJournal(filepath.Join(t.TempDir(), "undo"))
	if err != nil {
		t.Fatal(err)
	}
	account, err := lifecycle.CurrentAccount()
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "PathGuard.exe")
	policy := lifecycle.KnownLegacyPolicy(account, []string{executable})
	manager, err := lifecycle.NewLegacyManager(store, absentTasks{}, journal, policy)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := (lifecycle.Command{Executable: executable, Args: []string{"--different"}}).Value()
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ReplaceRun(ctx, lifecycle.LegacyRunName, lifecycle.RunValue{}, unknown); err != nil {
		t.Fatal(err)
	}
	entries, err := manager.Detect(ctx)
	if err != nil || entries[0].Ownership != lifecycle.Unknown {
		t.Fatal("same-name unknown entry claimed")
	}
	if _, err = manager.Disable(ctx, entries[0]); !errors.Is(err, lifecycle.ErrNotOwned) {
		t.Fatal("unknown entry disabled")
	}
	owned, err := (lifecycle.Command{Executable: executable, Args: []string{lifecycle.AutostartArgument}}).Value()
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ReplaceRun(ctx, lifecycle.LegacyRunName, unknown, owned); err != nil {
		t.Fatal(err)
	}
	entries, err = manager.Detect(ctx)
	if err != nil || entries[0].Ownership != lifecycle.Confirmed {
		t.Fatal("known legacy entry not detected")
	}
	change, err := manager.Disable(ctx, entries[0])
	if err != nil || !change.Changed || change.BackupID == "" {
		t.Fatalf("disable failed: %v", err)
	}
	backup, err := journal.Load(ctx, change.BackupID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.Undo(ctx, backup); err != nil {
		t.Fatal(err)
	}
	current, err := store.ReadRun(ctx, lifecycle.LegacyRunName)
	if err != nil || !current.Equal(owned) {
		t.Fatal("undo lost original bytes")
	}
	entries, err = manager.Detect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	change, err = manager.Disable(ctx, entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ReplaceRun(ctx, lifecycle.LegacyRunName, lifecycle.RunValue{}, unknown); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.Undo(ctx, change.Record); !errors.Is(err, lifecycle.ErrConflict) {
		t.Fatal("undo overwrote newer registration")
	}
}

func TestAT11TrayUnavailableAndSecondLaunch(t *testing.T) {
	shown, hidden, reported := 0, 0, 0
	controller, err := lifecycle.NewController(lifecycle.Hooks{Post: func(action func()) { action() }, ShowMain: func() { shown++ }, HideMain: func() { hidden++ }, OpenSettings: func() {}, BackupNow: func() {}, RequestExit: func() {}, ReportError: func(error) { reported++ }})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	controller.Start(lifecycle.AutostartLaunch, true, func(func(lifecycle.TrayAction)) (lifecycle.Tray, error) {
		return nil, errors.New("synthetic tray unavailable")
	})
	if shown != 1 || hidden != 0 || reported != 1 {
		t.Fatal("tray failure hid the only entry")
	}
	controller.SecondLaunch(lifecycle.ManualLaunch)
	if shown != 2 {
		t.Fatal("manual second launch did not restore window")
	}
	controller.SecondLaunch(lifecycle.AutostartLaunch)
	if shown != 2 {
		t.Fatal("duplicate autostart stole focus")
	}
}
