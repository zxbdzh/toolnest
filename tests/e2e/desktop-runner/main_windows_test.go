//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunnerNeverImportsForegroundActivation(t *testing.T) {
	code, err := os.ReadFile("main_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, dangerous := range []string{"SwitchDesktop", "SetForegroundWindow", "SendInput", "keybd_event", "mouse_event"} {
		if strings.Contains(string(code), `NewProc("`+dangerous+`")`) {
			t.Fatalf("private desktop runner gained forbidden activation API: %s", dangerous)
		}
	}
	if !strings.Contains(string(code), "CREATE_SUSPENDED") || !strings.Contains(string(code), "AssignProcessToJobObject") {
		t.Fatal("worker must be contained before it starts")
	}
}

func TestWorkerValidatesDesktopBeforeApplicationLaunch(t *testing.T) {
	for _, worker := range []struct{ filename, validation string }{
		{"Background-Worker.ps1", "Worker is not on its independently created desktop"},
		{"Update-Background-Worker.ps1", "Update worker is not on its independently created desktop"},
		{"Sandbox-Background-Worker.ps1", "Sandbox worker is not on its independently created desktop"},
		{"Installed-Kit-Worker.ps1", "Installed kit worker is not on its independently created desktop"},
	} {
		t.Run(worker.filename, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", worker.filename))
			if err != nil {
				t.Fatal(err)
			}
			code := string(data)
			validation := strings.Index(code, worker.validation)
			launch := strings.Index(code, "[Diagnostics.Process]::Start")
			if validation < 0 || launch < validation {
				t.Fatal("worker can launch before desktop validation")
			}
			for _, dangerous := range []string{"SwitchDesktop", "SetForegroundWindow", "SendInput", "keybd_event", "mouse_event", "SendKeys"} {
				if strings.Contains(code, dangerous) {
					t.Fatalf("private worker gained forbidden foreground input API: %s", dangerous)
				}
			}
		})
	}
}
