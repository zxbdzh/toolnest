package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"toolnest/internal/network"
)

// TestProcessEntry is an isolated executable entry equivalent to main's dispatch.
func TestProcessEntry(t *testing.T) {
	action := os.Getenv("TOOLNEST_TEST_PROCESS")
	if action == "" {
		return
	}
	if action == "parent" {
		time.Sleep(150 * time.Millisecond)
		os.Exit(0)
	}
	if action == "fail" {
		os.Exit(17)
	}
	var config struct{ Install, State, Key, Source string }
	if json.Unmarshal([]byte(os.Getenv("TOOLNEST_TEST_CONFIG")), &config) != nil {
		os.Exit(18)
	}
	source, err := HTTPTestSource(config.Source)
	if err != nil {
		os.Exit(19)
	}
	shared, err := network.New(context.Background(), network.Config{Mode: network.Direct}, nil)
	if err != nil {
		os.Exit(20)
	}
	manager, err := New(Config{CurrentVersion: "1.1.0", Source: source, PublicKey: config.Key, InstallDir: config.Install, StateDir: config.State, Network: shared, Gate: &LocalGate{}})
	if err != nil {
		os.Exit(21)
	}
	token := ""
	for index, argument := range os.Args {
		if argument == "--toolnest-update-confirm" && index+1 < len(os.Args) {
			token = os.Args[index+1]
		}
	}
	if err = manager.ConfirmStartup(context.Background(), token); err != nil {
		os.Exit(22)
	}
	time.Sleep(200 * time.Millisecond)
	os.Exit(0)
}

func configureRealExecutable(t *testing.T, fixture *fixture) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	fixture.archive = signedArchive(t, "1.1.0", "ToolNest.exe", content)
	digest := sha256.Sum256(fixture.archive)
	fixture.release.Size = int64(len(fixture.archive))
	fixture.release.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(fixture.private, digest[:]))
	config := fixture.manager.config
	data, err := json.Marshal(struct{ Install, State, Key, Source string }{config.InstallDir, config.StateDir, config.PublicKey, fixture.server.URL + "/manifest"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOOLNEST_TEST_CONFIG", string(data))
}

func TestRealExecutableStartupAndFailure(t *testing.T) {
	for _, action := range []string{"confirm", "fail"} {
		t.Run(action, func(t *testing.T) {
			fixture := newFixture(t)
			configureRealExecutable(t, fixture)
			t.Setenv("TOOLNEST_TEST_PROCESS", action)
			fixture.prepare()
			result, err := fixture.manager.Apply(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			err = fixture.manager.RestartInstalled(context.Background(), result, []string{"-test.run=^TestProcessEntry$", "--"}, 5*time.Second, nil)
			if action == "confirm" {
				if err != nil {
					t.Fatal(err)
				}
				if fixture.manager.Status().Phase != Confirmed {
					t.Fatal("real child never confirmed")
				}
			} else {
				if !errors.Is(err, ErrStartup) {
					t.Fatal(err)
				}
				fixture.assertOld()
			}
		})
	}
}

func TestRealParentExitAndHelper(t *testing.T) {
	fixture := newFixture(t)
	configureRealExecutable(t, fixture)
	fixture.prepare()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(executable, "-test.run=^TestProcessEntry$")
	parent.Env = append(os.Environ(), "TOOLNEST_TEST_PROCESS=parent")
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer parent.Wait()
	t.Setenv("TOOLNEST_TEST_PROCESS", "confirm")
	token, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixture.manager.config.StateDir, "helper-"+token+".json")
	request := HelperRequest{ParentPID: parent.Process.Pid, Token: token, Arguments: []string{"-test.run=^TestProcessEntry$", "--"}, Version: "1.1.0"}
	if err := writeJSON(path, request); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := fixture.manager.RunHelper(ctx, path, token, nil); err != nil {
		t.Fatal(err)
	}
	if fixture.manager.Status().Phase != Confirmed {
		t.Fatal("helper did not supervise actual new executable")
	}
}

func TestActualWindowsSharingViolation(t *testing.T) {
	fixture := newFixture(t)
	fixture.prepare()
	path, err := windows.UTF16PtrFromString(filepath.Join(fixture.manager.config.InstallDir, "ToolNest.exe"))
	if err != nil {
		t.Fatal(err)
	}
	// 不共享 DELETE，模拟其他进程占用程序；只触碰临时测试安装目录。
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if _, err := fixture.manager.Apply(context.Background()); err == nil {
		t.Fatal("occupied executable was replaced")
	}
	fixture.assertOld()
}

func TestWindowsStateLockReleasedByOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	release, err := lockState(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockState(path); !errors.Is(err, ErrBusy) {
		t.Fatal("concurrent process lock accepted")
	}
	release()
	release, err = lockState(path)
	if err != nil {
		t.Fatal("persistent lock file incorrectly blocks recovery")
	}
	release()
}
