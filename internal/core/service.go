package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrBusy = errors.New("正在执行关键操作，请完成后重试")
var ErrChanged = errors.New("PATH 在预览后发生变化，请重新预览并确认")

type Service struct {
	Registry Registry
	Store    Store
	Now      func() time.Time
	gate     sync.Mutex
}

func (service *Service) Backup(ctx context.Context, source string) (Snapshot, bool, error) {
	if !service.gate.TryLock() {
		return Snapshot{}, false, ErrBusy
	}
	defer service.gate.Unlock()
	state, err := service.Registry.Read(ctx)
	if err != nil {
		return Snapshot{}, false, err
	}
	if err := state.Validate(); err != nil {
		return Snapshot{}, false, err
	}
	if source != "manual" && source != "before-restore" {
		listing, err := service.Store.List()
		if err != nil {
			return Snapshot{}, false, err
		}
		if len(listing.Snapshots) > 0 && listing.Snapshots[0].State.Equal(state) {
			return listing.Snapshots[0], false, nil
		}
	}
	snapshot := NewSnapshot(state, source, service.Now())
	err = service.Store.Save(snapshot)
	return snapshot, err == nil, err
}

type RestorePlan struct {
	Token   string
	Target  Snapshot
	Before  State
	Scopes  []Scope
	Changes []Change
}
type RestoreResult struct {
	Outcome, ProtectionID string
	NotificationError     error
	Error                 error
	Scopes                []RestoreScopeResult
}

type RestoreScopeResult struct {
	Scope  Scope
	Status string
}

func (service *Service) Preview(ctx context.Context, id string, scopes []Scope) (RestorePlan, error) {
	if len(scopes) == 0 {
		return RestorePlan{}, errors.New("至少选择一个恢复范围")
	}
	for _, scope := range scopes {
		if scope != User && scope != System {
			return RestorePlan{}, errors.New("恢复范围不支持")
		}
	}
	snapshot, err := service.Store.Load(id)
	if err != nil {
		return RestorePlan{}, err
	}
	current, err := service.Registry.Read(ctx)
	if err != nil {
		return RestorePlan{}, err
	}
	return RestorePlan{NewID(), snapshot, current, append([]Scope(nil), scopes...), Diff(current, snapshot.State, scopes)}, nil
}

func (service *Service) Restore(ctx context.Context, plan RestorePlan) RestoreResult {
	if !service.gate.TryLock() {
		return RestoreResult{Outcome: "failed", Error: ErrBusy}
	}
	defer service.gate.Unlock()
	if plan.Token == "" || len(plan.Scopes) == 0 {
		return RestoreResult{Outcome: "failed", Error: errors.New("恢复预览已失效")}
	}
	if err := plan.Target.Validate(); err != nil {
		return RestoreResult{Outcome: "failed", Error: err}
	}
	release, err := service.Registry.Authorize(ctx, plan.Scopes)
	if err != nil {
		outcome := "failed"
		if errors.Is(err, ErrAuthorizationCancelled) || errors.Is(err, context.Canceled) {
			outcome = "cancelled"
		}
		return RestoreResult{Outcome: outcome, Error: err}
	}
	if release != nil {
		defer release()
	}
	current, err := service.Registry.Read(ctx)
	if err != nil {
		return RestoreResult{Outcome: "failed", Error: err}
	}
	if !current.Equal(plan.Before) {
		return RestoreResult{Outcome: "failed", Error: ErrChanged}
	}
	protection := NewSnapshot(current, "before-restore", service.Now())
	protection.OperationID = plan.Token
	if err := service.Store.Save(protection); err != nil {
		return RestoreResult{Outcome: "failed", Error: fmt.Errorf("保护快照写入失败，未修改 PATH: %w", err)}
	}
	result := RestoreResult{Outcome: "success", ProtectionID: protection.ID}
	for _, scope := range plan.Scopes {
		result.Scopes = append(result.Scopes, RestoreScopeResult{Scope: scope, Status: "not-written"})
	}
	setScope := func(scope Scope, status string) {
		for index := range result.Scopes {
			if result.Scopes[index].Scope == scope {
				result.Scopes[index].Status = status
			}
		}
	}
	var changed []Scope
	for _, scope := range plan.Scopes {
		// 即使写入返回错误，也必须读回判定该范围是否已经部分发生变化。
		changed = append(changed, scope)
		writeErr := service.Registry.Write(ctx, scope, plan.Target.State.Value(scope))
		readback, readErr := service.Registry.Read(ctx)
		if writeErr == nil && readErr == nil && readback.Value(scope).Equal(plan.Target.State.Value(scope)) {
			setScope(scope, "restored")
			continue
		}
		if writeErr == nil && readErr == nil {
			readErr = errors.New("PATH 写入后读回不一致")
		}
		result.Error = errors.Join(writeErr, readErr)
		result.Outcome = "rolled-back"
		for index := len(changed) - 1; index >= 0; index-- {
			affected := changed[index]
			latest, checkErr := service.Registry.Read(context.WithoutCancel(ctx))
			if checkErr != nil {
				setScope(affected, "unknown")
				result.Error = errors.Join(result.Error, checkErr)
				result.Outcome = "partial"
				continue
			}
			if latest.Value(affected).Equal(plan.Before.Value(affected)) {
				setScope(affected, "rolled-back")
				continue
			}
			if !latest.Value(affected).Equal(plan.Target.State.Value(affected)) {
				setScope(affected, "changed-externally")
				result.Outcome = "partial"
				result.Error = errors.Join(result.Error, errors.New("检测到外部修改，停止覆盖该范围"))
				continue
			}
			rollbackErr := service.Registry.Write(context.WithoutCancel(ctx), affected, plan.Before.Value(affected))
			verified, verifyErr := service.Registry.Read(context.WithoutCancel(ctx))
			if rollbackErr != nil || verifyErr != nil || !verified.Value(affected).Equal(plan.Before.Value(affected)) {
				setScope(affected, "rollback-incomplete")
				result.Outcome = "partial"
				result.Error = errors.Join(result.Error, rollbackErr, verifyErr, errors.New("回滚尚未完成"))
			} else {
				setScope(affected, "rolled-back")
			}
		}
		result.NotificationError = service.Registry.Notify(context.WithoutCancel(ctx))
		return result
	}
	result.NotificationError = service.Registry.Notify(context.WithoutCancel(ctx))
	return result
}
