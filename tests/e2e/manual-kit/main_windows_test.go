//go:build windows

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"toolnest/internal/core"
	"toolnest/tests/e2e/lab"
)

func TestInvalidIdentityCannotInitializeOrStartAnything(t *testing.T) {
	for _, arguments := range [][]string{nil, {"--serve"}, {"--check-maintenance"}, {"--stop"}, {"--set-mode", "good"}} {
		if err := run(arguments); err == nil {
			t.Fatalf("unbound helper accepted %v", arguments)
		}
	}
}

func TestManualSourceFaultSwitchAndGracefulStopWithoutApplication(t *testing.T) {
	previous := kitID
	kitID = "e28e75fe04664500b038f8cc94bf9b92"
	t.Cleanup(func() { kitID = previous })
	directory := t.TempDir()
	paths := locations{program: filepath.Join(directory, "program"), data: filepath.Join(directory, "data"), source: filepath.Join(directory, "source")}
	archive, err := lab.Archive([]byte("MZ-synthetic-service-test"), "0.1.4")
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	config := kitConfig{Baseline: "0.1.3", Target: "0.1.4", PublicKey: base64.StdEncoding.EncodeToString(public), Notes: "本地测试说明"}
	done := make(chan error, 1)
	go func() {
		done <- serveSource(paths, config, archive, base64.StdEncoding.EncodeToString(ed25519.Sign(private, digest[:])))
	}()
	t.Cleanup(func() {
		_ = core.AtomicWrite(filepath.Join(paths.source, "mode.txt"), []byte("stop"), true)
	})
	configuration := filepath.Join(paths.source, "fixture-private.json")
	for deadline := time.Now().Add(3 * time.Second); ; {
		if _, err := os.Stat(configuration); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("manual source did not become ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	previousKey := trustedPublicKey
	trustedPublicKey = config.PublicKey
	t.Cleanup(func() { trustedPublicKey = previousKey })
	connection, ok := healthy(paths)
	if !ok {
		t.Fatal("manual source is not healthy")
	}
	if err := core.AtomicWrite(filepath.Join(paths.source, "mode.txt"), []byte("source-not-found"), true); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	for deadline := time.Now().Add(3 * time.Second); ; {
		response, err := client.Get(connection.Feed)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode == http.StatusNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("manual failure switch did not take effect")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := core.AtomicWrite(filepath.Join(paths.source, "mode.txt"), []byte("stop"), true); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("manual source did not stop normally")
	}
}

func TestApplicationDigestAndRedirectBoundary(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "ToolNest.exe")
	if err := os.WriteFile(path, []byte("unexpected executable"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyApplication(path, kitConfig{}); err == nil {
		t.Fatal("unknown application accepted")
	}
	if !within(directory, path) || within(directory, directory+"-other\\ToolNest.exe") {
		t.Fatal("directory prefix escaped boundary")
	}
	for _, mode := range []string{"", "stop", "redirect", "https://example.com"} {
		if validMode(mode) {
			t.Fatal("unsupported control mode accepted")
		}
	}
}

func TestFirstInstallAcceptsOnlyMaintenanceLockButNotForeignFiles(t *testing.T) {
	directory := t.TempDir()
	paths := locations{program: directory, data: filepath.Join(t.TempDir(), "data")}
	release, err := maintenanceLock(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := checkIdle(paths); err != nil {
		t.Fatalf("first installer rejected its own lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "foreign.txt"), []byte("unowned"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkIdle(paths); err == nil {
		t.Fatal("occupied directory without kit ownership accepted")
	}
}

func TestStoppedSourceWithRetainedProcessObjectDoesNotBlockReinstall(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestInvalidIdentityCannotInitializeOrStartAnything$")
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "server.pid"), []byte(strconv.Itoa(command.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := stopSource(locations{source: directory, program: t.TempDir()}); err != nil {
		t.Fatalf("exited source blocked maintenance: %v", err)
	}
}
