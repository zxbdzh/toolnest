//go:build windows

package toolnest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"toolnest/internal/network"
	"toolnest/internal/update"
	"toolnest/packaging"
)

func TestManualBuildRefusesOrdinaryStartupBeforeEnvironment(t *testing.T) {
	previous := packaging.BuildChannel
	packaging.BuildChannel = "manual-test"
	t.Cleanup(func() { packaging.BuildChannel = previous })
	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 2 || !bytes.Contains(stderr.Bytes(), []byte("隔离启动")) {
		t.Fatalf("manual build entered ordinary startup: code=%d, error=%s", code, stderr.String())
	}
	stdout.Reset()
	if code := Run([]string{"--toolnest-build-info"}, &stdout, &stderr); code != 0 || !bytes.Contains(stdout.Bytes(), []byte("manual-test")) {
		t.Fatal("read-only identity must remain available without initialization")
	}
}

// 显式提供两个外部构建才运行真窗口测试；常规 go test 不弹窗、不编译覆盖产物。
func TestEntryPreparedUpdateAndRollback(t *testing.T) {
	if os.Getenv("TOOLNEST_ALLOW_FOREGROUND_TESTS") != "1" && !privateEntryDesktop() {
		t.Skip("desktop tests require a verified private desktop or explicit foreground authorization; ordinary checks never open windows")
	}
	oldExe, newExe := os.Getenv("TOOLNEST_ENTRY_TEST_EXE"), os.Getenv("TOOLNEST_ENTRY_TEST_NEW_EXE")
	if oldExe == "" || newExe == "" {
		t.Skip("set TOOLNEST_ENTRY_TEST_EXE and TOOLNEST_ENTRY_TEST_NEW_EXE to opt into real desktop update tests")
	}
	for _, scenario := range []struct {
		name          string
		failure, tray bool
	}{{"confirmed", false, false}, {"confirmed-in-tray", false, true}, {"startup-failed-rollback", true, false}} {
		t.Run(scenario.name, func(t *testing.T) {
			entryUpdateFixture(t, oldExe, newExe, scenario.failure, scenario.tray)
		})
	}
}

func privateEntryDesktop() bool {
	expected := os.Getenv("TOOLNEST_ENTRY_TEST_DESKTOP")
	if !regexp.MustCompile(`^ToolNest\.Test\.\d+\.\d+$`).MatchString(expected) {
		return false
	}
	// Go goroutine 可能换线程，查询桌面和线程标识必须来自同一个 OS 线程。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	user32 := windows.NewLazySystemDLL("user32.dll")
	threadID, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentThreadId").Call()
	desktop, _, _ := user32.NewProc("GetThreadDesktop").Call(threadID)
	input, _, _ := user32.NewProc("OpenInputDesktop").Call(0, 0, 1)
	if desktop == 0 || input == 0 {
		return false
	}
	defer user32.NewProc("CloseDesktop").Call(input)
	name := func(handle uintptr) string {
		buffer := make([]uint16, 256)
		var needed uint32
		ok, _, _ := user32.NewProc("GetUserObjectInformationW").Call(handle, 2, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)*2), uintptr(unsafe.Pointer(&needed)))
		if ok == 0 {
			return ""
		}
		return windows.UTF16ToString(buffer)
	}
	return name(desktop) == expected && name(input) != "" && name(input) != expected
}

func TestEntryDesktopCannotBeAuthorizedByEnvironmentAlone(t *testing.T) {
	t.Setenv("TOOLNEST_ENTRY_TEST_DESKTOP", "ToolNest.Test.123.456")
	if privateEntryDesktop() {
		t.Fatal("a forged desktop name authorized a window-producing test")
	}
}

func TestUpdateChildDoesNotSuppressMainWindow(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "startup.json")
	t.Setenv("TOOLNEST_STARTUP_PROBE", reportPath)
	process, err := launchProcess(os.Args[0], []string{"-test.run=^TestUpdateChildStartupProbe$"})
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var startup struct {
		Flags      uint32
		ShowWindow uint16
	}
	if err := json.Unmarshal(data, &startup); err != nil {
		t.Fatal(err)
	}
	if startup.Flags&windows.STARTF_USESHOWWINDOW != 0 && startup.ShowWindow == windows.SW_HIDE {
		t.Fatal("update child forces SW_HIDE on the first native window instead of only suppressing its console")
	}
}

func TestUpdateChildStartupProbe(t *testing.T) {
	reportPath := os.Getenv("TOOLNEST_STARTUP_PROBE")
	if reportPath == "" {
		t.Skip("startup metadata probe only runs as an explicitly requested child; no window is created")
	}
	var startup windows.StartupInfo
	if err := windows.GetStartupInfo(&startup); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(struct {
		Flags      uint32
		ShowWindow uint16
	}{startup.Flags, startup.ShowWindow})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProductionNeverAllowsInstalledTestOverrides(t *testing.T) {
	for _, channel := range []string{"production", "development", ""} {
		if allowInstalledIsolation(channel) {
			t.Fatalf("installed isolation enabled for %q", channel)
		}
	}
}

// 旧版助手退出后，新版可能短暂占用 helper-result.json。读取方必须容忍
// Windows 共享冲突并重试，不能把瞬时读失败当成上次更新失败展示。
func TestHelperResultReadToleratesSharingViolation(t *testing.T) {
	resultPath := filepath.Join(t.TempDir(), "helper-result.json")
	if err := os.WriteFile(resultPath, []byte(`{"error":"transient write"}`), 0600); err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(windows.StringToUTF16Ptr(resultPath), windows.GENERIC_READ,
		0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	var closeOnce sync.Once
	closeHandle := func() { closeOnce.Do(func() { _ = windows.CloseHandle(handle) }) }
	t.Cleanup(closeHandle)

	// 专有句柄先造成 ERROR_SHARING_VIOLATION，短延迟后释放，模拟原子写入收尾窗口。
	go func() {
		time.Sleep(80 * time.Millisecond)
		closeHandle()
	}()
	data, err := readHelperResult(resultPath)
	if err != nil {
		t.Fatalf("read did not tolerate sharing violation: %v", err)
	}
	var report struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &report) != nil || report.Error != "transient write" {
		t.Fatalf("unexpected helper result: %s", data)
	}
}

func TestHelperResultReadReportsPermanentOccupancy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper-result.json")
	if err := os.WriteFile(path, []byte(`{"error":"occupied"}`), 0600); err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(windows.StringToUTF16Ptr(path), windows.GENERIC_READ,
		0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(handle) })
	start := time.Now()
	if _, err := readHelperResult(path); !errors.Is(err, syscall.Errno(32)) {
		t.Fatalf("permanent occupancy must remain visible: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
		t.Fatalf("sharing violation returned too early: %s", elapsed)
	}
}

func TestHelperResultReadKeepsMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper-result.json")
	if _, err := readHelperResult(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing result must remain missing: %v", err)
	}
}

func entryUpdateFixture(t *testing.T, oldExe, newExe string, failStartup, tray bool) {
	t.Helper()
	root := t.TempDir()
	programs, data := filepath.Join(root, "programs"), filepath.Join(root, "data")
	if err := os.MkdirAll(programs, 0700); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(programs, "ToolNest.exe")
	if err := copyHelper(oldExe, installed); err != nil {
		t.Fatal(err)
	}
	oldDigest := entryDigest(t, installed)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicText := base64.StdEncoding.EncodeToString(public)
	keyFile := filepath.Join(root, "public.txt")
	if err := os.WriteFile(keyFile, []byte(publicText), 0600); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(newExe)
	if err != nil {
		t.Fatal(err)
	}
	if failStartup {
		payload = []byte("not a valid Windows executable; startup must fail")
	}
	baseline, target := entryVersion(t, oldExe), entryVersion(t, newExe)
	archive := entryArchive(t, payload, target)
	digest := sha256.Sum256(archive)
	release := update.Release{Version: target, Size: int64(len(archive)), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, digest[:]))}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/archive" {
			_, _ = writer.Write(archive)
			return
		}
		_ = json.NewEncoder(writer).Encode(release)
	}))
	defer server.Close()
	release.URL = server.URL + "/archive"
	source, err := update.HTTPTestSource(server.URL + "/manifest")
	if err != nil {
		t.Fatal(err)
	}
	netManager, err := network.New(context.Background(), network.Config{Mode: network.Direct}, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := update.New(update.Config{CurrentVersion: baseline, Source: source, PublicKey: publicText, InstallDir: programs,
		StateDir: filepath.Join(data, "updates"), Executable: "ToolNest.exe", Network: netManager, Gate: &update.LocalGate{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if _, err := manager.Check(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	arguments := []string{"--data-dir", data, "--test-root", hex.EncodeToString(random[:]), "--test-update-source", server.URL + "/manifest", "--test-update-key-file", keyFile, "--smoke"}
	if tray {
		arguments = append(arguments, "--autostart")
	}
	command := exec.CommandContext(ctx, installed, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("initial entry: %v\n%s", err, output)
	}
	resultPath := filepath.Join(data, "updates", "helper-result.json")
	for {
		result, err := readHelperResult(resultPath)
		if err == nil {
			var report struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(result, &report); err != nil {
				t.Fatal(err)
			}
			if failStartup {
				if report.Error == "" || entryDigest(t, installed) != oldDigest {
					t.Fatalf("failed startup did not restore old executable: %s", result)
				}
				if _, err := os.Stat(filepath.Join(data, "updates", "blocked.json")); err != nil {
					t.Fatal("failed release was not blocked", err)
				}
			} else if report.Error != "" || entryDigest(t, installed) != entryDigest(t, newExe) {
				t.Fatalf("native updated process did not confirm: %s", result)
			}
			// 确认后让 smoke 新版/回退版完成正常退出，避免测试删临时目录时文件仍占用。
			time.Sleep(3 * time.Second)
			t.Logf("entry helper result: %s", result)
			return
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("helper did not complete: %v; initial=%s", ctx.Err(), output)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func entryDigest(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func entryVersion(t *testing.T, executable string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, executable, "--toolnest-build-info").Output()
	if err != nil {
		t.Fatal("read fixture identity", err)
	}
	var identity struct{ Version, Channel string }
	if json.Unmarshal(output, &identity) != nil || !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(identity.Version) || (identity.Channel != "test" && identity.Channel != "manual-test") {
		t.Fatal("explicit versioned TEST-ONLY fixture required")
	}
	return identity.Version
}

func entryArchive(t *testing.T, payload []byte, version string) []byte {
	t.Helper()
	var data bytes.Buffer
	compressed := gzip.NewWriter(&data)
	archive := tar.NewWriter(compressed)
	descriptor, _ := json.Marshal(update.Descriptor{Version: version, Target: "windows-amd64", Executable: "ToolNest.exe"})
	for _, file := range []struct {
		name    string
		content []byte
	}{{"ToolNest.exe", payload}, {update.DescriptorName, descriptor}} {
		if err := archive.WriteHeader(&tar.Header{Name: file.name, Mode: 0700, Size: int64(len(file.content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(file.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
