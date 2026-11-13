package runtime

import (
	"path/filepath"
	"testing"
)

func TestRunOwnership(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "ToolNest.exe")
	command, err := RunCommand(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !OwnedRun(RunValue{RunName, 1, command}, exe) {
		t.Fatal("canonical command rejected")
	}
	if !OwnedRun(RunValue{RunName, 1, `"` + exe + `" "--autostart"`}, exe) {
		t.Fatal("application's quoted Run argument rejected")
	}
	for _, v := range []RunValue{{"Other", 1, command}, {RunName, 2, command}, {RunName, 1, command + " --extra"}, {RunName, 1, command + " "}, {RunName, 1, "ToolNest.exe --autostart"}} {
		if OwnedRun(v, exe) {
			t.Errorf("unowned Run accepted: %#v", v)
		}
	}
}
func TestRelativePath(t *testing.T) {
	if _, err := RunCommand("ToolNest.exe"); err == nil {
		t.Fatal("relative path accepted")
	}
}
