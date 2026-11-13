package update

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRollbackRetainsOnlyCurrentStartupFailureReason(t *testing.T) {
	fixture := newFixture(t)
	fixture.prepare()
	switched, err := fixture.manager.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	config := fixture.manager.config
	config.CurrentVersion = switched.Version
	candidate, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.RecordStartupFailure(strings.Repeat("0", 64), "forged reason"); !errors.Is(err, ErrStartup) {
		t.Fatal("unrelated attempt could write diagnostic")
	}
	reason := "找不到快照目录，请确认保存快照的磁盘是否已连接"
	if err := candidate.RecordStartupFailure(switched.Token, reason); err != nil {
		t.Fatal(err)
	}
	process := newFakeProcess()
	process.stop()
	if err := fixture.manager.RestartInstalled(context.Background(), switched, nil, time.Second, func(string, []string) (Process, error) {
		return process, nil
	}); !errors.Is(err, ErrStartup) {
		t.Fatal(err)
	}
	if err := fixture.manager.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := fixture.manager.Status()
	if !status.RolledBack || !strings.Contains(status.Failure, reason) || strings.Contains(status.Failure, "forged") {
		t.Fatalf("rollback lost its actionable startup reason: %+v", status)
	}
}
