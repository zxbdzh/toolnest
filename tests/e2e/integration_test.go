package e2e

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"toolnest/internal/core"
	"toolnest/internal/network"
	"toolnest/internal/update"
	"toolnest/tests/e2e/lab"
)

type syntheticRegistry struct {
	state        core.State
	writes       int
	deny         bool
	failWrite    bool
	failRollback bool
	notifyErr    bool
	target       core.Value
}

func (registry *syntheticRegistry) Read(context.Context) (core.State, error) {
	return registry.state, nil
}
func (registry *syntheticRegistry) Authorize(context.Context, []core.Scope) (func(), error) {
	if registry.deny {
		return nil, errors.New("synthetic authorization cancelled")
	}
	return func() {}, nil
}
func (registry *syntheticRegistry) Write(_ context.Context, scope core.Scope, value core.Value) error {
	registry.writes++
	if registry.failRollback && !value.Equal(registry.target) {
		return errors.New("synthetic rollback denied")
	}
	if scope == core.User {
		registry.state.User = value
	} else {
		registry.state.System = value
	}
	if registry.failWrite && value.Equal(registry.target) {
		return errors.New("synthetic post-write failure")
	}
	return nil
}
func (registry *syntheticRegistry) Notify(context.Context) error {
	if registry.notifyErr {
		return errors.New("synthetic notification failed")
	}
	return nil
}
func raw(text string, kind uint32) core.Value {
	units := utf16.Encode([]rune(text + "\x00"))
	data := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(data[index*2:], unit)
	}
	return core.Value{Exists: true, Type: kind, Bytes: data}
}
func serviceAt(t *testing.T, state core.State) (*core.Service, *syntheticRegistry) {
	t.Helper()
	registry := &syntheticRegistry{state: state}
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	return &core.Service{Registry: registry, Store: core.Store{Directory: t.TempDir()}, Now: func() time.Time { now = now.Add(time.Second); return now }}, registry
}

func TestAT01RawAndAT02OrderedSearch(t *testing.T) {
	state := core.State{User: raw(` C:\fixture ;;%SYNTHETIC%;C:\fixture;C:\fixture;`, 2), System: core.Value{Exists: true, Type: 1, Bytes: []byte{0, 0xd8, 1}}}
	service, _ := serviceAt(t, state)
	snapshot, saved, err := service.Backup(context.Background(), "manual")
	if err != nil || !saved {
		t.Fatalf("backup: %v", err)
	}
	loaded, err := service.Store.Load(snapshot.ID)
	if err != nil || !loaded.State.Equal(state) {
		t.Fatalf("raw data changed: %v", err)
	}
	entries := core.Filter(state.User, "fixture")
	if len(entries) != 3 || entries[0].Index != 1 || entries[1].Index != 4 || entries[2].Index != 5 {
		t.Fatalf("ordered filter: %+v", entries)
	}
	for _, value := range []core.Value{{}, {Exists: true, Type: 1, Bytes: []byte{}}, raw("", 1)} {
		snapshot := core.NewSnapshot(core.State{User: value}, "manual", time.Now())
		data, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := core.DecodeSnapshot(data)
		if err != nil || !decoded.User.Equal(value) {
			t.Fatalf("missing/empty collapsed: %v", err)
		}
	}
	if len(core.Diff(state, core.State{User: raw(`C:\fixture; C:\fixture ;;%SYNTHETIC%;C:\fixture;`, 2), System: state.System}, []core.Scope{core.User})) == 0 {
		t.Fatal("order change invisible")
	}
}

func TestAT03ManualAndAutomaticABA(t *testing.T) {
	initial := core.State{User: raw("A;A;", 1)}
	service, registry := serviceAt(t, initial)
	for index, source := range []string{"automatic", "automatic", "manual", "manual", "automatic", "automatic"} {
		if index == 4 {
			registry.state.User = raw("B", 1)
		}
		if index == 5 {
			registry.state = initial
		}
		_, saved, err := service.Backup(context.Background(), source)
		if err != nil || saved != (index != 1) {
			t.Fatalf("step %d saved=%v error=%v", index, saved, err)
		}
	}
	listing, err := service.Store.List()
	if err != nil || len(listing.Snapshots) != 5 {
		t.Fatalf("history: %v count=%d", err, len(listing.Snapshots))
	}
}

func TestAT04RejectBadSnapshots(t *testing.T) {
	for _, data := range []string{`{}`, `{"format_version":500}`, `{"format_version":1,"id":"../escape","created_at":"2026-11-12T00:00:00Z","reason":"manual","user":{},"system":{}}`} {
		if _, err := core.DecodeSnapshot([]byte(data)); err == nil {
			t.Fatal("unsafe snapshot accepted")
		}
	}
	directory := t.TempDir()
	filename := filepath.Join(directory, "oversized.json")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate(core.MaxSnapshotBytes + 1)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = core.LoadSnapshot(filename); err == nil {
		t.Fatal("oversized snapshot accepted")
	}
}

func TestAT05To07RestoreFailureBoundaries(t *testing.T) {
	for _, scenario := range []string{"cancel", "changed", "protection-failure", "rollback", "rollback-failure", "notify-failure"} {
		t.Run(scenario, func(t *testing.T) {
			service, registry := serviceAt(t, core.State{User: raw("target", 1), System: raw("system-unchanged", 2)})
			snapshot, _, err := service.Backup(context.Background(), "manual")
			if err != nil {
				t.Fatal(err)
			}
			registry.state.User = raw("current", 1)
			before := registry.state
			plan, err := service.Preview(context.Background(), snapshot.ID, []core.Scope{core.User})
			if err != nil {
				t.Fatal(err)
			}
			registry.target = snapshot.User
			switch scenario {
			case "cancel":
				registry.deny = true
			case "changed":
				registry.state.User = raw("external", 1)
			case "protection-failure":
				blocker := filepath.Join(t.TempDir(), "not-a-directory")
				if err = os.WriteFile(blocker, []byte("block"), 0600); err != nil {
					t.Fatal(err)
				}
				service.Store.Directory = blocker
			case "rollback":
				registry.failWrite = true
			case "rollback-failure":
				registry.failWrite = true
				registry.failRollback = true
			case "notify-failure":
				registry.notifyErr = true
			}
			result := service.Restore(context.Background(), plan)
			if !registry.state.System.Equal(before.System) {
				t.Fatal("unselected system modified")
			}
			switch scenario {
			case "cancel", "changed", "protection-failure":
				if registry.writes != 0 || result.Error == nil {
					t.Fatalf("must stop before writes: %+v writes=%d", result, registry.writes)
				}
			case "rollback":
				if result.Outcome != "rolled-back" || !registry.state.Equal(before) || result.ProtectionID == "" {
					t.Fatalf("rollback result %+v", result)
				}
			case "rollback-failure":
				if result.Outcome != "partial" || result.ProtectionID == "" {
					t.Fatalf("false success %+v", result)
				}
			case "notify-failure":
				if result.Outcome != "success" || result.NotificationError == nil || !registry.state.User.Equal(snapshot.User) {
					t.Fatalf("notification conflated %+v", result)
				}
			}
		})
	}
}

func TestAT08To09PersistedCatchupAndSingleRetry(t *testing.T) {
	now := time.Date(2026, 11, 12, 1, 0, 0, 0, time.FixedZone("fixture", 8*3600))
	var schedule core.Schedule
	if err := schedule.Configure(true, "interval", 6, 0, 0, now); err != nil {
		t.Fatal(err)
	}
	due, ok := schedule.Due(now)
	if !ok {
		t.Fatal("initial check missing")
	}
	schedule.Complete(due, now, "success")
	encoded, _ := json.Marshal(schedule)
	var reopened core.Schedule
	if err := json.Unmarshal(encoded, &reopened); err != nil {
		t.Fatal(err)
	}
	later := now.Add(48 * time.Hour)
	due, ok = reopened.Due(later)
	if !ok || due.Trigger != "catch-up" {
		t.Fatal("catch-up missing")
	}
	reopened.Complete(due, later, "failed")
	if _, ok = reopened.Due(later.Add(4 * time.Minute)); ok {
		t.Fatal("early retry")
	}
	due, ok = reopened.Due(later.Add(5 * time.Minute))
	if !ok {
		t.Fatal("retry missing")
	}
	reopened.Complete(due, later.Add(5*time.Minute), "failed")
	if !reopened.RetryAt.IsZero() {
		t.Fatal("recursive retry")
	}
	if _, ok = reopened.Due(later.Add(6 * time.Minute)); ok {
		t.Fatal("duplicate catch-up")
	}
	if err := reopened.Configure(false, "interval", 6, 0, 0, later); err != nil {
		t.Fatal(err)
	}
	if _, ok = reopened.Due(later.Add(100 * time.Hour)); ok {
		t.Fatal("disabled schedule runs")
	}
}

func TestAT08DailyClockRollbackAndTimezone(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.FixedZone("fixture-east", 8*3600))
	var schedule core.Schedule
	if err := schedule.Configure(true, "daily", 6, 9, 0, now); err != nil {
		t.Fatal(err)
	}
	due, ok := schedule.Due(now)
	if !ok {
		t.Fatal("daily initial missing")
	}
	schedule.Complete(due, now, "success")
	if _, ok = schedule.Due(now.Add(-time.Hour)); ok {
		t.Fatal("clock rollback repeated today's schedule")
	}
	later := now.Add(3 * 24 * time.Hour)
	due, ok = schedule.Due(later)
	if !ok {
		t.Fatal("missed daily schedule missing")
	}
	schedule.Complete(due, later, "success")
	if _, ok = schedule.Due(later); ok {
		t.Fatal("multiple catch-ups on same date")
	}
	changedZone := later.In(time.FixedZone("fixture-west", -8*3600))
	_, _ = schedule.Due(changedZone)
	if schedule.Next.IsZero() {
		t.Fatal("timezone change lost next deadline")
	}
}

func TestAT13To14ImportAndMovePreserveUnknown(t *testing.T) {
	source := core.Store{Directory: t.TempDir()}
	target := core.Store{Directory: t.TempDir()}
	snapshot := core.NewSnapshot(core.State{User: raw("synthetic", 1)}, "manual", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	if err := source.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source.Directory, "broken.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := target.Import(source.Directory)
	if err != nil || result.Copied != 1 || len(result.Failed) != 1 {
		t.Fatalf("import %+v %v", result, err)
	}
	result, err = target.Import(source.Directory)
	if err != nil || result.Skipped != 1 {
		t.Fatalf("repeat %+v %v", result, err)
	}
	loaded, err := target.Load(snapshot.ID)
	if err != nil || !loaded.CreatedAt.Equal(snapshot.CreatedAt) {
		t.Fatal("time lost")
	}
	unknown := filepath.Join(target.Directory, "unknown.keep")
	if err = os.WriteFile(unknown, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot.User = raw("conflict", 1)
	other := core.Store{Directory: t.TempDir()}
	if err = other.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	result, err = target.Import(other.Directory)
	if err != nil || len(result.Failed) != 1 {
		t.Fatal("conflict overwritten")
	}
	if data, _ := os.ReadFile(unknown); string(data) != "preserve" {
		t.Fatal("unknown data lost")
	}
}

type fixtureVault struct{ secret network.Credentials }

func (vault fixtureVault) Read(context.Context, string) (network.Credentials, error) {
	return vault.secret, nil
}
func (fixtureVault) Write(context.Context, string, network.Credentials) error { return nil }
func (fixtureVault) Delete(context.Context, string) error                     { return nil }

func updateManager(t *testing.T, environment *lab.Environment, mode, address string, valid bool) (*update.Manager, *update.LocalGate, string) {
	t.Helper()
	configuration := network.Config{Mode: network.Mode(mode)}
	secret := environment.Password
	if !valid {
		secret += "invalid"
	}
	var vault network.Vault
	if mode == "custom" {
		configuration.Address = address
		configuration.CredentialID = "fixture"
		vault = fixtureVault{network.Credentials{Username: environment.Username, Password: secret}}
	}
	manager, err := network.New(context.Background(), configuration, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Snapshot().CloseIdleConnections() })
	source, err := update.HTTPTestSource(environment.Feed + "/manifest")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	install := filepath.Join(root, "program")
	if err = os.Mkdir(install, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(install, "ToolNest.exe"), []byte("MZ-SYNTHETIC-0.1.0"), 0600); err != nil {
		t.Fatal(err)
	}
	gate := &update.LocalGate{}
	updater, err := update.New(update.Config{CurrentVersion: "0.1.0", Source: source, PublicKey: environment.PublicKey, InstallDir: install, StateDir: filepath.Join(root, "state"), Network: manager, Gate: gate})
	if err != nil {
		t.Fatal(err)
	}
	return updater, gate, install
}

func TestAT16AuthenticatedPoliciesAndRedirect(t *testing.T) {
	environment, err := lab.Start([]byte("MZ-SYNTHETIC-0.1.1"))
	if err != nil {
		t.Fatal(err)
	}
	defer environment.Close()
	for _, protocol := range []string{"http", "socks5"} {
		t.Run(protocol, func(t *testing.T) {
			address := environment.HTTPProxy
			if protocol == "socks5" {
				address = environment.SOCKSProxy
			}
			updater, _, _ := updateManager(t, environment, "custom", address, true)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if _, err := updater.Check(ctx, true); err != nil {
				t.Fatal(err)
			}
			if _, err := updater.Prepare(ctx); err != nil {
				t.Fatal(err)
			}
			if updater.Status().Phase != update.Ready {
				t.Fatal("not ready")
			}
		})
	}
	if environment.HTTPRequests.Load() < 4 || environment.SOCKSRequests.Load() < 1 || environment.LeakedCredentials.Load() {
		t.Fatalf("proxy bypass or credential leak: %s", environment)
	}
	for _, address := range []string{environment.HTTPProxy, environment.SOCKSProxy} {
		updater, _, _ := updateManager(t, environment, "custom", address, false)
		before := environment.OriginRequests.Load()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err := updater.Check(ctx, true)
		cancel()
		if err == nil || environment.OriginRequests.Load() != before {
			t.Fatal("failed auth bypassed proxy")
		}
		if strings.Contains(err.Error(), environment.Password) || strings.Contains(err.Error(), environment.Username) {
			t.Fatal("credentials exposed")
		}
	}
}

func TestAT19CorruptionOfflineBusyAndRollback(t *testing.T) {
	for _, fault := range []string{"bad-signature", "disconnect", "truncated", "http-error", "busy", "startup-blocked", "rollback-failure"} {
		t.Run(fault, func(t *testing.T) {
			environment, err := lab.Start([]byte("MZ-SYNTHETIC-0.1.1"))
			if err != nil {
				t.Fatal(err)
			}
			defer environment.Close()
			updater, gate, install := updateManager(t, environment, "direct", "", true)
			data := filepath.Join(filepath.Dir(install), "user-data.keep")
			if err = os.WriteFile(data, []byte("retain"), 0600); err != nil {
				t.Fatal(err)
			}
			if fault != "busy" && fault != "startup-blocked" && fault != "rollback-failure" {
				if err = environment.SetMode(fault); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if _, err = updater.Check(ctx, true); err != nil {
				t.Fatal(err)
			}
			_, err = updater.Prepare(ctx)
			if fault != "busy" && fault != "startup-blocked" && fault != "rollback-failure" {
				if err == nil || updater.Prepared() != nil {
					t.Fatal("fault accepted")
				}
				if fault == "bad-signature" && !errors.Is(err, update.ErrSignature) {
					t.Fatalf("wrong signature result: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if fault == "busy" {
					release, err := gate.TryAcquire("backup")
					if err != nil {
						t.Fatal(err)
					}
					_, err = updater.Apply(ctx)
					release()
					if !errors.Is(err, update.ErrBusy) {
						t.Fatalf("busy replaced program: %v", err)
					}
				} else {
					result, err := updater.Apply(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if fault == "rollback-failure" {
						if err = os.WriteFile(result.Backup, []byte("tampered retained backup"), 0600); err != nil {
							t.Fatal(err)
						}
					}
					err = updater.RestartInstalled(ctx, result, nil, time.Second, func(string, []string) (update.Process, error) { return nil, errors.New("synthetic launch blocked") })
					if fault == "startup-blocked" && !errors.Is(err, update.ErrStartup) {
						t.Fatalf("rollback: %v", err)
					}
					if fault == "rollback-failure" && !errors.Is(err, update.ErrRecovery) {
						t.Fatalf("false recovery: %v", err)
					}
					if fault == "startup-blocked" {
						if _, err = updater.Check(ctx, false); !errors.Is(err, update.ErrBlocked) {
							t.Fatalf("bad version loops: %v", err)
						}
					}
				}
			}
			if kept, _ := os.ReadFile(data); string(kept) != "retain" {
				t.Fatal("user data changed")
			}
			if fault != "rollback-failure" {
				current, err := os.ReadFile(filepath.Join(install, "ToolNest.exe"))
				if err != nil || !bytes.Equal(current, []byte("MZ-SYNTHETIC-0.1.0")) {
					t.Fatalf("old version not preserved: %v", err)
				}
			}
		})
	}
}
