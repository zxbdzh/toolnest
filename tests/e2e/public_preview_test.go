package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"toolnest/internal/network"
	"toolnest/internal/update"
)

func TestOptionalPublicPreviewGitHubDownloadAndSignature(t *testing.T) {
	keyFile := os.Getenv("TOOLNEST_PUBLIC_PREVIEW_KEY_FILE")
	if keyFile == "" {
		t.Skip("explicit independent public preview key required; ordinary tests remain offline")
	}
	public, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	archiveFile := os.Getenv("TOOLNEST_PUBLIC_PREVIEW_ARCHIVE")
	expectedArchive, err := os.ReadFile(archiveFile)
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := gzip.NewReader(bytes.NewReader(expectedArchive))
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	var expectedExecutable []byte
	for {
		entry, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == "ToolNest.exe" {
			expectedExecutable, err = io.ReadAll(io.LimitReader(reader, 512<<20))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(expectedExecutable) < 1<<20 {
		t.Fatal("actual independently verified executable required")
	}
	networkManager, err := network.New(context.Background(), network.Config{Mode: network.System}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer networkManager.Snapshot().CloseIdleConnections()
	source, err := update.GitHub("zxbdzh/toolnest")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	program := filepath.Join(root, "program")
	if err := os.Mkdir(program, 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("MZ-INDEPENDENT-DOWNLOAD-PROBE-NEVER-EXECUTED")
	target := filepath.Join(program, "ToolNest.exe")
	if err := os.WriteFile(target, original, 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := update.New(update.Config{CurrentVersion: "0.0.0", Source: source, PublicKey: strings.TrimSpace(string(public)),
		InstallDir: program, StateDir: filepath.Join(root, "updates"), Executable: "ToolNest.exe", Network: networkManager, Gate: &update.LocalGate{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	release, err := manager.Check(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if release == nil || release.Version != "0.0.1" || !strings.HasPrefix(release.Notes, "# ToolNest 0.0.1\n") {
		t.Fatal("first public source did not return matching version and notes")
	}
	prepared, err := manager.Prepare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(prepared.Executable)
	if err != nil || !bytes.Equal(staged, expectedExecutable) {
		t.Fatal("public download differs from independently verified release")
	}
	retained, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(retained, original) || manager.Status().Phase != update.Ready {
		t.Fatal("download probe modified installed executable or failed to stage")
	}
	t.Log("Public GitHub check, notes, download, signature and exact executable match passed; no installation or GUI started.")
}
