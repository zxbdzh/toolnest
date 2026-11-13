//go:build windows

package lifecycle

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func uniqueTestID(t *testing.T) string {
	t.Helper()
	var b [12]byte
	if _, e := rand.Read(b[:]); e != nil {
		t.Fatal(e)
	}
	return hex.EncodeToString(b[:])
}
func isolatedRun(t *testing.T) (*WindowsRunStore, string) {
	t.Helper()
	path := `Software\ToolNest\Tests\Lifecycle-` + uniqueTestID(t)
	store, e := NewIsolatedWindowsRunStore(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := registry.DeleteKey(registry.CURRENT_USER, path); e != nil && !errors.Is(e, registry.ErrNotExist) {
			t.Error(e)
		}
	})
	return store, path
}
func TestWindowsRunOnlyIsolatedHKCU(t *testing.T) {
	s, path := isolatedRun(t)
	a, e := CurrentAccount()
	if e != nil {
		t.Fatal(e)
	}
	j := &fakeJournal{}
	m, e := NewRunManager(s, j, a, testExe)
	if e != nil {
		t.Fatal(e)
	}
	if enabled, e := m.Enabled(context.Background()); e != nil || enabled {
		t.Fatal(enabled, e)
	}
	change, e := m.SetEnabled(context.Background(), true)
	if e != nil {
		t.Fatal(e)
	}
	if enabled, e := m.Enabled(context.Background()); e != nil || !enabled {
		t.Fatal(enabled, e)
	}
	read, e := s.ReadRun(context.Background(), ToolNestRunName)
	if e != nil || !read.Equal(change.Record.AfterRun) {
		t.Fatal(read, e)
	}
	if _, e = m.Undo(context.Background(), change.Record); e != nil {
		t.Fatal(e)
	}
	// 精确检查类型，不能把 REG_EXPAND_SZ 当成自己的 REG_SZ。
	k, _, e := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if e != nil {
		t.Fatal(e)
	}
	line, _ := decodeRun(change.Record.AfterRun)
	if e = k.SetExpandStringValue(ToolNestRunName, line); e != nil {
		k.Close()
		t.Fatal(e)
	}
	k.Close()
	if _, e = m.SetEnabled(context.Background(), false); !errors.Is(e, ErrNotOwned) {
		t.Fatal(e)
	}
	if e = s.ReplaceRun(context.Background(), "Unrelated", RunValue{}, RunValue{}); !errors.Is(e, ErrNotOwned) {
		t.Fatal(e)
	}
}
func TestWindowsIsolatedStoreRejectsRealKeys(t *testing.T) {
	for _, s := range []string{currentRunKey, `Environment`, `Software\ToolNest\Tests`, `Software\ToolNest\Tests\..\Run`, `Software\ToolNest\Tests\`, `Software/ToolNest/Tests/x`} {
		if _, e := NewIsolatedWindowsRunStore(s); e == nil {
			t.Fatalf("accepted unsafe test key %q", s)
		}
	}
}
func TestWindowsInstanceManualWakeAndSilentAutostart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	name := "ToolNest.Tests." + uniqueTestID(t)
	got := make(chan Launch, 4)
	primary, secondary, e := AcquireInstance(ctx, name, ManualLaunch, func(l Launch) { got <- l })
	if e != nil || secondary {
		t.Fatal(secondary, e)
	}
	defer primary.Close()
	other, secondary, e := AcquireInstance(ctx, name, AutostartLaunch, func(Launch) { t.Error("secondary callback") })
	if e != nil || !secondary || other != nil {
		t.Fatal(secondary, e)
	}
	select {
	case <-got:
		t.Fatal("autostart disturbed primary")
	case <-time.After(50 * time.Millisecond):
	}
	other, secondary, e = AcquireInstance(ctx, name, ManualLaunch, func(Launch) { t.Error("secondary callback") })
	if e != nil || !secondary || other != nil {
		t.Fatal(secondary, e)
	}
	select {
	case l := <-got:
		if l != ManualLaunch {
			t.Fatal(l)
		}
	case <-ctx.Done():
		t.Fatal("manual activation lost")
	}
	primary.Close()
	again, secondary, e := AcquireInstance(ctx, name, ManualLaunch, func(Launch) {})
	if e != nil || secondary {
		t.Fatal(secondary, e)
	}
	again.Close()
}
func TestWindowsTaskScriptParsesWithoutExecuting(t *testing.T) {
	system, e := windows.GetSystemDirectory()
	if e != nil {
		t.Fatal(e)
	}
	shell := filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe")
	script := filepath.Join(t.TempDir(), "tasks.ps1")
	if e = os.WriteFile(script, []byte(taskScript), 0600); e != nil {
		t.Fatal(e)
	}
	// 只调用 PowerShell 语法解析器，不执行嵌入脚本，不连接 Task Scheduler COM。
	parser := `[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false);$tokens=$null;$errors=$null;$text=[IO.File]::ReadAllText($env:TOOLNEST_TEST_SCRIPT,[Text.Encoding]::UTF8);[void][System.Management.Automation.Language.Parser]::ParseInput($text,[ref]$tokens,[ref]$errors);if($errors.Count -gt 0){$errors | ForEach-Object { [Console]::WriteLine([string]$_.Extent.StartLineNumber+': '+$_.Message) };exit 1}`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodedScript(parser))
	cmd.Env = append(os.Environ(), "TOOLNEST_TEST_SCRIPT="+script)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("script parse: %v %s", e, out)
	}
	for _, forbidden := range []string{"Invoke-Expression", "RegisterTask", "DeleteTask", "GetTasks(", "Stop-Process", ".Run("} {
		if strings.Contains(taskScript, forbidden) {
			t.Fatalf("unrestricted script operation: %s", forbidden)
		}
	}
}
