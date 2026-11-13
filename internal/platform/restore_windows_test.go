//go:build windows

package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
	"toolnest/internal/core"
)

type failSystemOnce struct {
	rawStore
	failed bool
}

func (s *failSystemOnce) write(ctx context.Context, scope core.Scope, value core.Value) error {
	err := s.rawStore.write(ctx, scope, value)
	if err == nil && scope == core.System && !s.failed {
		s.failed = true
		return errors.New("模拟系统写入后返回失败")
	}
	return err
}

func TestWindowsCoreRestoreTransaction(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback-after-partial-failure"}[fail], func(t *testing.T) {
			r, raw := isolatedRegistry(t)
			ctx := context.Background()
			before := sampleState()
			for _, scope := range []core.Scope{core.User, core.System} {
				if err := raw.write(ctx, scope, before.Value(scope)); err != nil {
					t.Fatal(err)
				}
			}
			service := core.Service{Registry: r, Store: core.Store{Directory: t.TempDir()}, Now: time.Now}
			target := samplePlan(before, core.User, core.System).Target
			if err := service.Store.Save(target); err != nil {
				t.Fatal(err)
			}
			plan, err := service.Preview(ctx, target.ID, []core.Scope{core.User, core.System})
			if err != nil {
				t.Fatal(err)
			}
			if err = r.ConfirmRestore(plan); err != nil {
				t.Fatal(err)
			}
			if fail {
				r.store = &failSystemOnce{rawStore: raw}
			}
			result := service.Restore(ctx, plan)
			want := "success"
			state := target.State
			if fail {
				want = "rolled-back"
				state = before
			}
			if result.Outcome != want || result.ProtectionID == "" {
				t.Fatalf("%+v", result)
			}
			if fail && result.Error == nil {
				t.Fatal("missing injected failure")
			}
			actual, err := r.Read(ctx)
			if err != nil || !actual.Equal(state) {
				t.Fatal(actual, err)
			}
			protection, err := service.Store.Load(result.ProtectionID)
			if err != nil || !protection.State.Equal(before) || protection.OperationID != plan.Token {
				t.Fatal(protection, err)
			}
		})
	}
}
func TestProtectionSnapshotFailureHasZeroRegistryWrites(t *testing.T) {
	r, raw := isolatedRegistry(t)
	ctx := context.Background()
	before := sampleState()
	for _, scope := range []core.Scope{core.User, core.System} {
		if err := raw.write(ctx, scope, before.Value(scope)); err != nil {
			t.Fatal(err)
		}
	}
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	service := core.Service{Registry: r, Store: core.Store{Directory: blocked}, Now: time.Now}
	plan := samplePlan(before, core.User, core.System)
	if err := r.ConfirmRestore(plan); err != nil {
		t.Fatal(err)
	}
	result := service.Restore(ctx, plan)
	if result.Outcome != "failed" || result.Error == nil {
		t.Fatal(result)
	}
	actual, err := r.Read(ctx)
	if err != nil || !actual.Equal(before) {
		t.Fatal(actual, err)
	}
}
