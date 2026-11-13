package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"toolnest/internal/core"
	"toolnest/internal/network"
)

type testRegistry struct{ state core.State }

func (registry *testRegistry) Read(context.Context) (core.State, error)       { return registry.state, nil }
func (*testRegistry) Authorize(context.Context, []core.Scope) (func(), error) { return func() {}, nil }
func (registry *testRegistry) Write(_ context.Context, scope core.Scope, value core.Value) error {
	if scope == core.User {
		registry.state.User = value
	} else {
		registry.state.System = value
	}
	return nil
}
func (*testRegistry) Notify(context.Context) error { return nil }

type testVault struct {
	values map[string]network.Credentials
}

func (vault *testVault) Read(_ context.Context, id string) (network.Credentials, error) {
	value, ok := vault.values[id]
	if !ok {
		return value, errors.New("missing")
	}
	return value, nil
}
func (vault *testVault) Write(_ context.Context, id string, value network.Credentials) error {
	vault.values[id] = value
	return nil
}
func (vault *testVault) Delete(_ context.Context, id string) error {
	delete(vault.values, id)
	return nil
}

func testController(t *testing.T) *Controller {
	t.Helper()
	t.Setenv("LOCALAPPDATA", t.TempDir())
	controller, err := New(Options{Root: t.TempDir(), Version: "0.1.0", Registry: &testRegistry{}, Vault: &testVault{values: map[string]network.Credentials{}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(controller.cancel)
	return controller
}

func TestDefaultsSkipAndReopen(t *testing.T) {
	controller := testController(t)
	settings := controller.View().Settings
	if settings.ScheduleEnabled || settings.LaunchAtLogin || settings.IntervalHours != 6 || settings.OnboardingDone {
		t.Fatal(settings)
	}
	settings.OnboardingDone = true
	if err := controller.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{Root: controller.root, Version: "0.1.0", Registry: &testRegistry{}})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.cancel()
	if !reopened.View().Settings.OnboardingDone || reopened.View().Settings.ScheduleEnabled {
		t.Fatal(reopened.View().Settings)
	}
}

func TestSettingsFailureKeepsOldAndCredentialsStayPrivate(t *testing.T) {
	controller := testController(t)
	old := controller.View().Settings
	invalid := old
	invalid.IntervalHours = 0
	if err := controller.saveSettings(context.Background(), invalid); err == nil {
		t.Fatal("invalid settings accepted")
	}
	if controller.View().Settings.IntervalHours != old.IntervalHours {
		t.Fatal("invalid draft committed")
	}
	settings := old
	settings.ProxyMode = "custom"
	settings.ProxyURL = "http://127.0.0.1:8989"
	settings.ProxyUsername = "test"
	settings.ProxyPassword = "very-secret-password"
	if err := controller.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(controller.root, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), settings.ProxyPassword) || controller.View().Settings.ProxyPassword != "" {
		t.Fatal("password leaked")
	}
	if controller.config.CredentialID == "" {
		t.Fatal("missing credential reference")
	}
}

func TestBusyPreventsWriteAndSafeExit(t *testing.T) {
	controller := testController(t)
	quits := 0
	controller.hooks.Quit = func() { quits++ }
	release, err := controller.TryAcquire("恢复 PATH")
	if err != nil {
		t.Fatal(err)
	}
	controller.Quit()
	if quits != 0 || controller.closed {
		t.Fatal("quit while busy")
	}
	if _, err := controller.TryAcquire("更新替换"); err == nil {
		t.Fatal("concurrent mutation")
	}
	controller.CancelExit()
	release()
	controller.Quit()
	if quits != 1 || !controller.closed {
		t.Fatal("safe quit failed")
	}
}

func TestSchedulePersistsInitialAndUnchanged(t *testing.T) {
	controller := testController(t)
	settings := controller.View().Settings
	settings.ScheduleEnabled = true
	if err := controller.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	controller.tick(time.Now())
	if len(controller.View().Snapshots) != 1 {
		t.Fatal("initial snapshot missing")
	}
	next := controller.config.Schedule.Next
	controller.tick(next)
	if len(controller.View().Snapshots) != 1 {
		t.Fatal("unchanged duplicated")
	}
	view := controller.View()
	if len(view.Records) != 2 || view.Records[1].Outcome != "unchanged" {
		t.Fatal(view.Records)
	}
}

func TestUnavailableDirectoryNotReportedEmpty(t *testing.T) {
	controller := testController(t)
	controller.tick(time.Now())
	controller.service.Store.Directory = filepath.Join(controller.root, "offline")
	if err := controller.refresh(context.Background()); err == nil || controller.View().Error == "" {
		t.Fatal("offline directory hidden")
	}
}

func TestInterruptedSettingsReplacementRecoversPrevious(t *testing.T) {
	controller := testController(t)
	settings := controller.View().Settings
	settings.OnboardingDone = true
	if err := controller.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	settings.Theme = "dark"
	if err := controller.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(controller.root, "settings.json")
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	recovered, err := loadConfig(controller.root)
	if err != nil || !recovered.Settings.OnboardingDone || recovered.Settings.Theme != "system" {
		t.Fatal(recovered, err)
	}
}

func TestMissingProxyCredentialDoesNotBlockOfflinePath(t *testing.T) {
	controller := testController(t)
	settings := controller.View().Settings
	settings.ProxyMode = "custom"
	settings.ProxyURL = "http://127.0.0.1:9898"
	settings.ProxyUsername = "name"
	settings.ProxyPassword = "secret"
	if err := controller.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{Root: controller.root, Version: "0.1.0", Registry: &testRegistry{}, Vault: &testVault{values: map[string]network.Credentials{}}})
	if err != nil {
		t.Fatal("offline app blocked", err)
	}
	defer reopened.cancel()
	if reopened.network != nil || reopened.View().Error == "" {
		t.Fatal("must report blocked network without direct fallback")
	}
	settings = reopened.View().Settings
	settings.Theme = "dark"
	if err := reopened.saveSettings(context.Background(), settings); err != nil {
		t.Fatal("offline appearance save required proxy credentials", err)
	}
	if reopened.network != nil {
		t.Fatal("appearance save silently created a direct client")
	}
}

func TestCorruptedFallbackCannotEnableInvalidSchedule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"broken":`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "settings.json.previous"), []byte(`{"version":1,"settings":{"ScheduleEnabled":true},"schedule":{"enabled":true,"mode":"interval","hours":0}}`), 0600); err != nil {
		t.Fatal(err)
	}
	configuration, err := loadConfig(root)
	if err == nil || configuration.Settings.ScheduleEnabled || configuration.Schedule.Enabled {
		t.Fatal("invalid fallback activated schedule", configuration, err)
	}
}

func TestProxyDistinguishesMissingUpdateSourceFromConnectionFailure(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte("private-server-body"))
	}))
	defer origin.Close()
	controller := testController(t)
	controller.updateEndpoint = origin.URL + "/releases/latest?token=private-query"
	before := controller.View().Settings
	draft := before
	draft.ProxyMode = "direct"
	controller.TestProxy(draft)
	deadline := time.Now().Add(3 * time.Second)
	for controller.View().ProxyTesting && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	view := controller.View()
	if view.ProxyTesting || !strings.Contains(view.Error, "HTTP 404") || !strings.Contains(view.Error, "不代表代理失效") || strings.Contains(view.Error, "连接失败") {
		t.Fatalf("missing update source was blamed on proxy: %+v", view)
	}
	if !strings.Contains(view.Status, "更新源不可用") || view.Settings != before || strings.Contains(view.Error, "private") {
		t.Fatal("connection diagnosis changed saved settings or disclosed private material")
	}
}
