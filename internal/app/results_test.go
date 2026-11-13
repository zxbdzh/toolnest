package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"toolnest/internal/core"
)

func waitOperation(t *testing.T, controller *Controller) View {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); ; {
		view := controller.View()
		if view.Busy == "" {
			return view
		}
		if time.Now().After(deadline) {
			t.Fatal("operation did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFirstBackupRestoreAndUndoKeepDurableResults(t *testing.T) {
	controller := testController(t)
	registry := controller.service.Registry.(*testRegistry)
	registry.state.User = core.Value{Exists: true, Type: 1, Bytes: []byte{'A', 0, 0, 0}}
	controller.CompleteOnboardingWithBackup(controller.View().Settings, true)
	first := waitOperation(t, controller)
	if first.Error != "" || first.Result.Title != "第一份快照已保存" || first.Result.SnapshotID == "" || !first.Settings.OnboardingDone || len(first.Snapshots) != 1 {
		t.Fatalf("first use did not produce a verified snapshot and result: %+v", first.Result)
	}
	registry.state.User.Bytes = []byte{'B', 0, 0, 0}
	preview, err := controller.PreviewRestore(first.Result.SnapshotID, true, false)
	if err != nil {
		t.Fatal(err)
	}
	controller.Restore(preview)
	restored := waitOperation(t, controller)
	if restored.Result.ProtectionID == "" || restored.Result.Outcome != "success" || len(restored.Result.Scopes) != 1 || restored.Result.Scopes[0].Status != "已恢复并核对" || registry.state.User.Text() != "A" {
		t.Fatalf("restore result incomplete: %+v", restored.Result)
	}
	undo, err := controller.PreviewRestore(restored.Result.ProtectionID, true, false)
	if err != nil {
		t.Fatal(err)
	}
	controller.Restore(undo)
	undone := waitOperation(t, controller)
	if undone.Result.Outcome != "success" || registry.state.User.Text() != "B" {
		t.Fatal("protection snapshot did not restore the pre-operation bytes")
	}
	reopened, err := New(Options{Root: controller.root, Version: "0.1.5", Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.cancel()
	found := false
	for _, record := range reopened.View().Records {
		if record.Result != nil && record.Result.ProtectionID == restored.Result.ProtectionID {
			found = true
		}
	}
	if !found {
		t.Fatal("reopening lost restore result/protection linkage")
	}
}

func TestDataFailureHasRecoveryActionAndDoesNotCreateReplacement(t *testing.T) {
	controller := testController(t)
	missing := filepath.Join(t.TempDir(), "removed-store")
	controller.service.Store.Directory = missing
	if err := controller.refresh(context.Background()); err == nil {
		t.Fatal("missing store should report a problem")
	}
	view := controller.View()
	if len(view.Problems) != 1 || view.Problems[0].Code != "snapshot-directory-missing" || view.Problems[0].Action != "data" {
		t.Fatalf("missing recovery action: %+v", view.Problems)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refresh recreated a missing store")
	}
}

func TestRestorePresentationNeverClaimsUntouchedSystemWasRestored(t *testing.T) {
	plan := core.RestorePlan{Scopes: []core.Scope{core.User, core.System}}
	result := restoreResult(plan, core.RestoreResult{Outcome: "partial", ProtectionID: "protected", Scopes: []core.RestoreScopeResult{
		{Scope: core.User, Status: "changed-externally"}, {Scope: core.System, Status: "not-written"},
	}})
	if result.Scopes[0].Status != "检测到其他程序修改，已停止覆盖" || result.Scopes[1].Status != "未写入" || result.ProtectionID == "" {
		t.Fatal("partial restore was flattened into generic success")
	}
}
