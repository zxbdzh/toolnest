//go:build windows

package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolatedMaintenanceLock(t *testing.T) {
	// 只测试临时锁文件，不创建生产目录、不访问 Run 或卸载登记。
	path := filepath.Join(t.TempDir(), "maintenance.lock")
	release, err := acquireLockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if other, err := acquireLockFile(path); !errors.Is(err, ErrBusy) {
		if other != nil {
			other()
		}
		t.Fatalf("concurrent lock must be busy: %v", err)
	}
	release()
	release() // 关闭必须幂等，不能误关后续复用的 Windows handle。
	again, err := acquireLockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	again()
}

func TestRejectLinkedMaintenanceLock(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original")
	link := filepath.Join(dir, "linked")
	if err := os.WriteFile(original, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, link); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if release, err := acquireLockFile(link); err == nil {
		release()
		t.Fatal("hard-linked lock accepted")
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "preserve" {
		t.Fatal("original file changed")
	}
}

func TestRunArgumentsAreCaseSensitive(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "ToolNest.exe")
	command, err := RunCommand(exe)
	if err != nil {
		t.Fatal(err)
	}
	if OwnedRun(RunValue{RunName, 1, strings.Replace(command, RunArguments, "--AUTOSTART", 1)}, exe) {
		t.Fatal("different argument spelling accepted")
	}
	if !OwnedRun(RunValue{RunName, 1, strings.ToUpper(command[:len(command)-len(RunArguments)]) + RunArguments}, exe) {
		t.Fatal("Windows path case should not affect ownership")
	}
}
