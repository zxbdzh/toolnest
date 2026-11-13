package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"toolnest/internal/core"
	"toolnest/internal/update"
)

type failingReadRegistry struct {
	core.Registry
	err error
}

func (registry *failingReadRegistry) Read(ctx context.Context) (core.State, error) {
	if registry.err != nil {
		return core.State{}, registry.err
	}
	return registry.Registry.Read(ctx)
}

func TestAutomaticRecoveryClearsOnlyItsError(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	controller := testController(t)
	settings := controller.View().Settings
	settings.ScheduleEnabled = true
	if err := controller.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	controller.tick(time.Now())
	registry := &failingReadRegistry{Registry: controller.service.Registry, err: errors.New("测试读取失败")}
	controller.service.Registry = registry
	controller.tick(controller.config.Schedule.Next)
	if controller.View().Error == "" {
		t.Fatal("automatic failure missing")
	}
	registry.err = nil
	controller.tick(controller.config.Schedule.RetryAt)
	if controller.View().Error != "" {
		t.Fatal("successful retry left the old error active")
	}
	controller.setError(errors.New("另一个操作仍失败"))
	controller.tick(controller.config.Schedule.Next)
	if controller.View().Error != "另一个操作仍失败" {
		t.Fatal("automatic recovery erased an unrelated failure")
	}
}

func TestBusyExitCompletesAfterWriteWithoutSecondRequest(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	controller := testController(t)
	var quits atomic.Int32
	controller.hooks.Quit = func() { quits.Add(1) }
	release, err := controller.TryAcquire("恢复 PATH")
	if err != nil {
		t.Fatal(err)
	}
	controller.Quit()
	if quits.Load() != 0 {
		t.Fatal("exit interrupted a write")
	}
	release()
	deadline := time.Now().Add(time.Second)
	for quits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if quits.Load() != 1 {
		t.Fatal("exit request was silently discarded")
	}
}

func TestCancelledExitDoesNotCloseAfterWrite(t *testing.T) {
	controller := testController(t)
	var quits atomic.Int32
	controller.hooks.Quit = func() { quits.Add(1) }
	release, err := controller.TryAcquire("恢复 PATH")
	if err != nil {
		t.Fatal(err)
	}
	controller.Quit()
	if controller.View().PendingExit != "quit" {
		t.Fatal("waiting exit is not visible")
	}
	controller.CancelExit()
	release()
	controller.mu.RLock()
	generation := controller.exitGeneration
	controller.mu.RUnlock()
	controller.completeExit(generation - 1)
	if quits.Load() != 0 || controller.View().PendingExit != "" {
		t.Fatal("cancelled exit still closed the application")
	}
}

func TestOnlyExplicitRestartInvokesUpdateHook(t *testing.T) {
	for _, intent := range []string{"quit", "restart"} {
		t.Run(intent, func(t *testing.T) {
			controller := testController(t)
			var restarts, quits int
			controller.hooks.RestartUpdate = func() error { restarts++; return nil }
			controller.hooks.Quit = func() { quits++ }
			controller.requestExit(intent)
			if quits != 1 || restarts != map[string]int{"quit": 0, "restart": 1}[intent] {
				t.Fatalf("exit intent %s: restarts=%d quits=%d", intent, restarts, quits)
			}
		})
	}
}

func TestSharedGateRecoveryBeforeDesktopStartup(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	root := t.TempDir()
	source, err := update.HTTPTestSource("http://127.0.0.1:1/manifest")
	if err != nil {
		t.Fatal(err)
	}
	controller, err := New(Options{Root: root, Version: "0.1.0", Registry: &testRegistry{},
		Source: source, PublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32)), InstallDir: filepath.Join(root, "program")})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.cancel()
	if err := controller.Updater().Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if controller.View().Busy != "" {
		t.Fatal("startup recovery left the application busy")
	}
}

func TestDisablingAutomaticDownloadDuringCheckIsRespected(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	started, proceed, finished := make(chan struct{}, 1), make(chan struct{}), make(chan struct{})
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/manifest" {
			downloads.Add(1)
			writer.WriteHeader(503)
			return
		}
		started <- struct{}{}
		select {
		case <-proceed:
		case <-request.Context().Done():
			return
		}
		_ = json.NewEncoder(writer).Encode(update.Release{Version: "0.1.1", URL: "http://" + request.Host + "/payload", Size: 100, Signature: base64.StdEncoding.EncodeToString(make([]byte, 64))})
	}))
	defer server.Close()
	source, err := update.HTTPTestSource(server.URL + "/manifest")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	controller, err := New(Options{Root: root, Version: "0.1.0", Registry: &testRegistry{}, Source: source,
		PublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32)), InstallDir: filepath.Join(root, "program")})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.cancel()
	go func() { controller.checkUpdate(true); close(finished) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("check did not start")
	}
	settings := controller.View().Settings
	settings.AutoDownload = false
	if err := controller.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("check did not finish")
	}
	if downloads.Load() != 0 || controller.View().Update.State != "available" {
		t.Fatal("background download ignored the changed preference")
	}
}
