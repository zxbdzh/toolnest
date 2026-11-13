//go:build windows

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"toolnest/internal/network"
	"toolnest/internal/update"
	"toolnest/tests/e2e/lab"
)

// TestMain 只为本测试二进制提供受控子进程入口，不冒充产品主程序。
func TestMain(suite *testing.M) {
	if os.Getenv("TOOLNEST_ACCEPTANCE_CHILD") == "1" {
		var config struct {
			Install, State, Key, Source string
			Fail                        bool
		}
		if json.Unmarshal([]byte(os.Getenv("TOOLNEST_ACCEPTANCE_CONFIG")), &config) != nil {
			os.Exit(31)
		}
		if config.Fail {
			os.Exit(32)
		}
		source, err := update.HTTPTestSource(config.Source)
		if err != nil {
			os.Exit(33)
		}
		networkManager, err := network.New(context.Background(), network.Config{Mode: network.Direct}, nil)
		if err != nil {
			os.Exit(34)
		}
		manager, err := update.New(update.Config{CurrentVersion: "0.1.1", Source: source, PublicKey: config.Key, InstallDir: config.Install, StateDir: config.State, Network: networkManager, Gate: &update.LocalGate{}})
		if err != nil {
			os.Exit(35)
		}
		for index, argument := range os.Args {
			if argument == "--toolnest-update-confirm" && index+1 < len(os.Args) {
				if err = manager.ConfirmStartup(context.Background(), os.Args[index+1]); err != nil {
					os.Exit(36)
				}
				time.Sleep(300 * time.Millisecond)
				os.Exit(0)
			}
		}
		os.Exit(37)
	}
	os.Exit(suite.Run())
}

func TestAT19ActualWindowsSharingViolation(t *testing.T) {
	hostGuard(t)
	environment, err := lab.Start([]byte("MZ-SYNTHETIC-0.1.1"))
	if err != nil {
		t.Fatal(err)
	}
	defer environment.Close()
	updater, _, install := updateManager(t, environment, "direct", "", true)
	ctx := context.Background()
	if _, err = updater.Check(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err = updater.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	filename, err := windows.UTF16PtrFromString(filepath.Join(install, "ToolNest.exe"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(filename, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, applyErr := updater.Apply(ctx)
	_ = windows.CloseHandle(handle)
	if applyErr == nil {
		t.Fatal("locked executable replaced")
	}
	data, err := os.ReadFile(filepath.Join(install, "ToolNest.exe"))
	if err != nil || !bytes.Equal(data, []byte("MZ-SYNTHETIC-0.1.0")) {
		t.Fatal("sharing violation lost old executable")
	}
}

func TestAT18BoundaryActualChildConfirmationAndCrash(t *testing.T) {
	hostGuard(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	for _, fail := range []bool{false, true} {
		name := "confirmation"
		if fail {
			name = "crash"
		}
		t.Run(name, func(t *testing.T) {
			environment, err := lab.Start(content)
			if err != nil {
				t.Fatal(err)
			}
			defer environment.Close()
			updater, _, install := updateManager(t, environment, "custom", environment.SOCKSProxy, true)
			configuration := struct {
				Install, State, Key, Source string
				Fail                        bool
			}{install, filepath.Join(filepath.Dir(install), "state"), environment.PublicKey, environment.Feed + "/manifest", fail}
			encoded, err := json.Marshal(configuration)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("TOOLNEST_ACCEPTANCE_CHILD", "1")
			t.Setenv("TOOLNEST_ACCEPTANCE_CONFIG", string(encoded))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err = updater.Check(ctx, true); err != nil {
				t.Fatal(err)
			}
			if _, err = updater.Prepare(ctx); err != nil {
				t.Fatal(err)
			}
			result, err := updater.Apply(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var child *exec.Cmd
			launcher := func(path string, arguments []string) (update.Process, error) {
				child = exec.Command(path, arguments...)
				child.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				if err := child.Start(); err != nil {
					return nil, err
				}
				return &testChild{child}, nil
			}
			err = updater.RestartInstalled(ctx, result, nil, 5*time.Second, launcher)
			if fail {
				if !errors.Is(err, update.ErrStartup) {
					t.Fatalf("crash falsely accepted: %v", err)
				}
			} else if err != nil || updater.Status().Phase != update.Confirmed {
				t.Fatalf("real child confirmation: %v phase=%v", err, updater.Status().Phase)
			}
			// 监督器已启动 Wait；等待子进程自行结束，不强杀写入，也不挂后台服务。
			if child != nil {
				deadline := time.Now().Add(2 * time.Second)
				for time.Now().Before(deadline) {
					handle, openErr := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(child.Process.Pid))
					if openErr != nil {
						break
					}
					event, _ := windows.WaitForSingleObject(handle, 50)
					_ = windows.CloseHandle(handle)
					if event == windows.WAIT_OBJECT_0 {
						break
					}
				}
			}
		})
	}
}

type testChild struct{ command *exec.Cmd }

func (child *testChild) Wait() error { return child.command.Wait() }
func (child *testChild) Kill() error { return child.command.Process.Kill() }
