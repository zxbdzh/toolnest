package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReservedAcceptanceBoundaries(t *testing.T) {
	for _, path := range []string{filepath.Join(acceptanceRoot(), `run\private`), `C:\ToolNestAcceptance\work\private`} {
		if !reserved(path) {
			t.Errorf("reserved directory rejected: %s", path)
		}
	}
	for _, path := range []string{filepath.Join(t.TempDir(), "repository", "keys"), filepath.Join(os.TempDir(), `toolnest-acceptance-escape\keys`), filepath.Join(acceptanceRoot(), `..\production`), acceptanceRoot(), `C:\ToolNestAcceptance\evidence\private`, `relative`} {
		if reserved(path) {
			t.Errorf("unsafe directory accepted: %s", path)
		}
	}
}

func TestRunnerRejectsUnsafeOutputBeforeKeyOrServer(t *testing.T) {
	for _, operation := range []string{"keygen", "sign", "serve"} {
		if err := run([]string{"--operation", operation, "--out", filepath.Join(t.TempDir(), "repository", "unsafe-output")}); err == nil {
			t.Fatal("unsafe output accepted")
		}
	}
}
