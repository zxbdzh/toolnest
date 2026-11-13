package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pathValue(text string) Value {
	value := Value{Exists: true, Type: 2, Bytes: []byte{}}
	for _, character := range text {
		value.Bytes = append(value.Bytes, byte(character), byte(character>>8))
	}
	value.Bytes = append(value.Bytes, 0, 0)
	return value
}

type fakeRegistry struct {
	state     State
	writes    int
	denied    bool
	failAt    int
	corrupt   bool
	notifyErr bool
}

func (registry *fakeRegistry) Read(context.Context) (State, error) { return registry.state, nil }
func (registry *fakeRegistry) Authorize(context.Context, []Scope) (func(), error) {
	if registry.denied {
		return nil, ErrAuthorizationCancelled
	}
	return func() {}, nil
}
func (registry *fakeRegistry) Write(_ context.Context, scope Scope, value Value) error {
	registry.writes++
	if registry.failAt == registry.writes {
		return errors.New("注入写入失败")
	}
	if registry.corrupt {
		value = pathValue("unrelated external change")
	}
	if scope == User {
		registry.state.User = value
	} else {
		registry.state.System = value
	}
	return nil
}
func (registry *fakeRegistry) Notify(context.Context) error {
	if registry.notifyErr {
		return errors.New("通知失败")
	}
	return nil
}

func newService(t *testing.T) (*Service, *fakeRegistry) {
	t.Helper()
	registry := &fakeRegistry{state: State{pathValue("A;;A; %TEMP%;"), pathValue("B")}}
	service := &Service{Registry: registry, Store: Store{t.TempDir()}, Now: time.Now}
	return service, registry
}

func TestRawRoundTripAndMissing(t *testing.T) {
	values := []Value{{}, {Exists: true, Type: 1, Bytes: []byte{}}, pathValue(""), pathValue("A;;A; %TEMP%;"), {Exists: true, Type: 2, Bytes: []byte{0, 0xd8, 1}}}
	for _, value := range values {
		snapshot := NewSnapshot(State{value, value}, "manual", time.Now())
		data, _ := json.Marshal(snapshot)
		loaded, err := DecodeSnapshot(data)
		if err != nil || !loaded.State.Equal(snapshot.State) {
			t.Fatalf("raw roundtrip: %v", err)
		}
		_ = value.Text()
	}
	if values[0].Equal(values[1]) || values[1].Equal(values[2]) {
		t.Fatal("missing/empty merged")
	}
}

func TestLegacyStrictValidation(t *testing.T) {
	good := `{"id":"legacy-1","created_at":"2026-11-12T01:00:00Z","reason":"manual","user":{"registry_type":2,"bytes":[65,0,0,0]},"system":null}`
	loaded, err := DecodeSnapshot([]byte(good))
	if err != nil || loaded.System.Exists || loaded.User.Text() != "A" {
		t.Fatalf("legacy: %v", err)
	}
	for _, invalid := range []string{strings.Replace(good, `,"system":null`, "", 1), strings.Replace(good, `"registry_type":2,`, "", 1), strings.Replace(good, `"bytes":[65,0,0,0]`, `"bytes":null`, 1), strings.Replace(good, `"id":"legacy-1"`, `"id":"../oops"`, 1)} {
		if _, err := DecodeSnapshot([]byte(invalid)); err == nil {
			t.Fatal("accepted unsafe legacy")
		}
	}
	data, _ := json.Marshal(NewSnapshot(State{pathValue("x"), Value{}}, "manual", time.Now()))
	for _, invalid := range []string{strings.Replace(string(data), `"exists":true,`, "", 1), strings.Replace(string(data), `"format_version":1`, `"format_version":99`, 1)} {
		if _, err := DecodeSnapshot([]byte(invalid)); err == nil {
			t.Fatal("accepted incomplete new snapshot")
		}
	}
}

func TestLegacyImportWritesCurrentFormatAndSkipsRepeat(t *testing.T) {
	source := t.TempDir()
	legacy := []byte(`{"id":"old-1","created_at":"2026-11-12T01:00:00Z","reason":"manual","user":{"registry_type":2,"bytes":[65,0,0,0]},"system":null}`)
	if err := os.WriteFile(filepath.Join(source, "old-1.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	target := Store{Directory: t.TempDir()}
	first, err := target.Import(source)
	if err != nil || first.Copied != 1 || len(first.Failed) > 0 {
		t.Fatal(first, err)
	}
	loaded, err := target.Load("old-1")
	if err != nil || loaded.Version != FormatVersion || loaded.User.Text() != "A" || loaded.Origin != "PathGuard:old-1" {
		t.Fatal(loaded, err)
	}
	second, err := target.Import(source)
	if err != nil || second.Skipped != 1 || len(second.Failed) > 0 {
		t.Fatal(second, err)
	}
}

func TestFilterAndDirectionalDiff(t *testing.T) {
	value := pathValue("B;A;A;; C ")
	filtered := Filter(value, "A")
	if len(filtered) != 2 || filtered[0].Index != 2 || filtered[1].Index != 3 {
		t.Fatal(filtered)
	}
	before := State{value, Value{}}
	after := State{pathValue("A;B;A; C ;"), Value{}}
	changes := Diff(before, after, []Scope{User})
	if len(changes) != 1 || changes[0].Before != value.Text() {
		t.Fatal(changes)
	}
}

func TestBackupManualAndAutomaticABA(t *testing.T) {
	service, registry := newService(t)
	ctx := context.Background()
	for _, source := range []string{"manual", "manual", "automatic"} {
		_, saved, err := service.Backup(ctx, source)
		if err != nil || saved != (source == "manual") {
			t.Fatalf("%s %t %v", source, saved, err)
		}
	}
	original := registry.state
	registry.state.User = pathValue("different")
	if _, saved, err := service.Backup(ctx, "automatic"); err != nil || !saved {
		t.Fatal(err)
	}
	registry.state = original
	if _, saved, err := service.Backup(ctx, "automatic"); err != nil || !saved {
		t.Fatal(err)
	}
	listing, _ := service.Store.List()
	if len(listing.Snapshots) != 4 {
		t.Fatal(len(listing.Snapshots))
	}
	if registry.writes != 0 {
		t.Fatal("backup wrote registry")
	}
}

func TestRestoreCancellationStaleProtectionAndRollback(t *testing.T) {
	for _, scenario := range []string{"cancel", "stale", "protection", "rollback", "external", "notification"} {
		t.Run(scenario, func(t *testing.T) {
			service, registry := newService(t)
			ctx := context.Background()
			target, _, _ := service.Backup(ctx, "manual")
			registry.state = State{pathValue("new user"), pathValue("new system")}
			before := registry.state
			plan, err := service.Preview(ctx, target.ID, []Scope{User, System})
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "cancel":
				registry.denied = true
			case "stale":
				registry.state.User = pathValue("changed again")
			case "protection":
				service.Store.Directory = filepath.Join(t.TempDir(), "file")
				os.WriteFile(service.Store.Directory, []byte("x"), 0600)
			case "rollback":
				registry.failAt = 2
			case "external":
				registry.corrupt = true
			case "notification":
				registry.notifyErr = true
			}
			result := service.Restore(ctx, plan)
			switch scenario {
			case "cancel", "stale", "protection":
				if registry.writes != 0 || result.Outcome == "success" {
					t.Fatal(result, registry.writes)
				}
			case "rollback":
				if result.Outcome != "rolled-back" || !registry.state.Equal(before) || result.ProtectionID == "" {
					t.Fatal(result)
				}
			case "external":
				if result.Outcome != "partial" {
					t.Fatal(result)
				}
			case "notification":
				if result.Outcome != "success" || result.NotificationError == nil || !registry.state.Equal(target.State) {
					t.Fatal(result)
				}
			}
		})
	}
}

func TestImportAndDirectorySwitchPreserveUnknown(t *testing.T) {
	service, _ := newService(t)
	original, _, _ := service.Backup(context.Background(), "manual")
	target := Store{t.TempDir()}
	first, err := target.Import(service.Store.Directory)
	if err != nil || first.Copied != 1 {
		t.Fatal(first, err)
	}
	second, err := target.Import(service.Store.Directory)
	if err != nil || second.Skipped != 1 {
		t.Fatal(second, err)
	}
	original.User = pathValue("conflict")
	data, _ := json.Marshal(original)
	os.WriteFile(filepath.Join(target.Directory, original.ID+".json"), data, 0600)
	result, err := service.Store.CopyTo(target.Directory)
	if err == nil || len(result.Failed) != 1 {
		t.Fatal(result, err)
	}
	if _, err := service.Store.Load(original.ID); err != nil {
		t.Fatal("source removed", err)
	}
	if _, err := service.Store.CopyTo(filepath.Join(service.Store.Directory, "nested")); err == nil {
		t.Fatal("accepted overlapping directory")
	}
}

func TestScheduleCatchupRetryAndRevision(t *testing.T) {
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	schedule := Schedule{Mode: "interval", Hours: 6}
	if err := schedule.Configure(true, "interval", 6, 9, 0, now); err != nil {
		t.Fatal(err)
	}
	due, ok := schedule.Due(now)
	if !ok {
		t.Fatal("initial check missing")
	}
	schedule.Complete(due, now, "failed")
	if !schedule.RetryAt.Equal(now.Add(5 * time.Minute)) {
		t.Fatal(schedule)
	}
	due, ok = schedule.Due(now.Add(5 * time.Minute))
	if !ok || due.Trigger != "retry" {
		t.Fatal(due)
	}
	schedule.Complete(due, now.Add(5*time.Minute), "failed")
	if !schedule.RetryAt.IsZero() {
		t.Fatal("recursive retry")
	}
	due, ok = schedule.Due(now.Add(31 * time.Hour))
	if !ok || due.Trigger != "catch-up" {
		t.Fatal(due)
	}
	schedule.Complete(due, now.Add(31*time.Hour), "unchanged")
	if !schedule.Next.After(now.Add(31 * time.Hour)) {
		t.Fatal(schedule)
	}
	stale := due
	schedule.Configure(false, "interval", 6, 9, 0, now)
	schedule.Complete(stale, now, "failed")
	if !schedule.RetryAt.IsZero() {
		t.Fatal("stale job applied")
	}
}

func TestDailyClockRollbackAndZoneChange(t *testing.T) {
	zone := time.FixedZone("local", 8*3600)
	now := time.Date(2026, 11, 12, 9, 30, 0, 0, zone)
	schedule := Schedule{Mode: "daily", Hours: 6, Hour: 9}
	schedule.Configure(true, "daily", 6, 9, 0, now)
	due, _ := schedule.Due(now)
	schedule.Complete(due, now, "success")
	if _, ok := schedule.Due(now.Add(-time.Hour)); ok {
		t.Fatal("clock rollback duplicated daily")
	}
	nextZone := time.FixedZone("different", 9*3600)
	if _, ok := schedule.Due(now.In(nextZone)); ok {
		t.Fatal("zone change duplicated date")
	}
	if _, ok := schedule.Due(now.Add(72 * time.Hour).In(nextZone)); !ok {
		t.Fatal("sleep catchup missing")
	}
}

func TestEachScheduledFailureHasOneIndependentRetry(t *testing.T) {
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	schedule := Schedule{Mode: "interval", Hours: 1}
	schedule.Configure(true, "interval", 1, 0, 0, now)
	first, _ := schedule.Due(now)
	schedule.Complete(first, now, "failed")
	retry, _ := schedule.Due(now.Add(5 * time.Minute))
	schedule.Complete(retry, now.Add(5*time.Minute), "failed")
	second, ok := schedule.Due(now.Add(time.Hour))
	if !ok {
		t.Fatal("second due missing")
	}
	schedule.Complete(second, now.Add(time.Hour), "failed")
	if !schedule.RetryAt.Equal(now.Add(65 * time.Minute)) {
		t.Fatal("next occurrence lost retry budget", schedule)
	}
}

func TestUnavailableSnapshotDirectoryIsNotRecreated(t *testing.T) {
	service, _ := newService(t)
	service.Store.Directory = filepath.Join(t.TempDir(), "offline")
	if _, _, err := service.Backup(context.Background(), "manual"); err == nil {
		t.Fatal("offline directory silently recreated")
	}
	if _, err := os.Stat(service.Store.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("offline path was mutated", err)
	}
}

func TestDailyRetryAcrossMidnightKeepsNextNormalRun(t *testing.T) {
	now := time.Date(2026, 11, 12, 23, 58, 0, 0, time.UTC)
	schedule := Schedule{Mode: "daily", Hours: 6, Hour: 23, Minute: 58}
	schedule.Configure(true, "daily", 6, 23, 58, now)
	due, _ := schedule.Due(now)
	schedule.Complete(due, now, "failed")
	retry, ok := schedule.Due(now.Add(5 * time.Minute))
	if !ok || retry.Trigger != "retry" {
		t.Fatal(retry)
	}
	schedule.Complete(retry, now.Add(5*time.Minute), "success")
	expected := time.Date(2026, 11, 13, 23, 58, 0, 0, time.UTC)
	if !schedule.Next.Equal(expected) {
		t.Fatal("retry skipped next normal day", schedule.Next)
	}
}
