// Package platform implements ToolNest's restricted Windows boundary.
package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"toolnest/internal/core"
)

var (
	ErrNotConfirmed = errors.New("必须先绑定用户已确认的恢复预览")
	ErrUnauthorized = errors.New("写入不在本次已确认授权内")
	ErrCancelled    = core.ErrAuthorizationCancelled
	ErrUnsupported  = errors.New("此平台不支持 Windows 适配")
)

// Options 不接受任意注册表路径。TestRoot 只能是新的 32 位十六进制测试标识。
type Options struct{ TestRoot string }

var testID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (o Options) validate() error {
	if o.TestRoot != "" && !testID.MatchString(o.TestRoot) {
		return errors.New("TestRoot 必须是 32 位小写十六进制隔离标识")
	}
	return nil
}

type authorization struct {
	Version     int          `json:"version"`
	OriginalSID string       `json:"original_sid"`
	Token       string       `json:"confirmation_token"`
	Scopes      []core.Scope `json:"scopes"`
	Before      core.State   `json:"before"`
	Target      core.State   `json:"target"`
	TestRoot    string       `json:"test_root,omitempty"`
}

func (a authorization) validate() error {
	if a.Version != 1 || a.OriginalSID == "" || a.Token == "" || len(a.Token) > 160 || strings.ContainsAny(a.Token, "\x00\r\n") {
		return ErrNotConfirmed
	}
	if err := (Options{TestRoot: a.TestRoot}).validate(); err != nil {
		return err
	}
	if err := a.Before.Validate(); err != nil {
		return err
	}
	if err := a.Target.Validate(); err != nil {
		return err
	}
	if len(a.Scopes) == 0 || len(a.Scopes) > 2 {
		return ErrNotConfirmed
	}
	seen := map[core.Scope]bool{}
	for _, s := range a.Scopes {
		if (s != core.User && s != core.System) || seen[s] {
			return ErrNotConfirmed
		}
		seen[s] = true
	}
	return nil
}
func (a authorization) digest() string {
	b, _ := json.Marshal(a)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func cloneState(s core.State) core.State {
	s.User.Bytes = slices.Clone(s.User.Bytes)
	s.System.Bytes = slices.Clone(s.System.Bytes)
	return s
}

type rawStore interface {
	read(context.Context) (core.State, error)
	write(context.Context, core.Scope, core.Value) error
	notify(context.Context) error
}
type helperSession interface {
	write(context.Context, core.Scope, core.Value) error
	close()
}
type helperStarter func(context.Context, authorization) (helperSession, error)

// Registry 满足 core.Registry；构造和 Read 绝不创建环境键或 PATH 值。
type Registry struct {
	mu         sync.Mutex
	store      rawStore
	options    Options
	sid        string
	confirmed  *authorization
	active     *authorization
	helper     helperSession
	start      helperStarter
	written    map[core.Scope]bool
	rolledBack map[core.Scope]bool
}

var _ core.Registry = (*Registry)(nil)

// ConfirmRestore 由 UI 在展示并确认 plan 后调用，紧接着调用 core.Service.Restore。
// core.Registry.Authorize 不携带目标值，因此额外绑定不可省略，否则助手会沦为任意 PATH 写入口。
func (r *Registry) ConfirmRestore(plan core.RestorePlan) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil {
		return core.ErrBusy
	}
	if err := plan.Target.Validate(); err != nil {
		return err
	}
	a := authorization{Version: 1, OriginalSID: r.sid, Token: plan.Token, Scopes: slices.Clone(plan.Scopes), Before: cloneState(plan.Before), Target: cloneState(plan.Target.State), TestRoot: r.options.TestRoot}
	if err := a.validate(); err != nil {
		return err
	}
	r.confirmed = &a
	return nil
}
func (r *Registry) Read(ctx context.Context) (core.State, error) {
	if err := ctx.Err(); err != nil {
		return core.State{}, err
	}
	return r.store.read(ctx)
}

func (r *Registry) Authorize(ctx context.Context, scopes []core.Scope) (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil {
		return nil, core.ErrBusy
	}
	a := r.confirmed
	r.confirmed = nil // 确认令牌只消费一次，取消后必须重新确认。
	if a == nil || !slices.Equal(a.Scopes, scopes) {
		return nil, ErrNotConfirmed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, err := r.store.read(ctx)
	if err != nil {
		return nil, err
	}
	if !current.Equal(a.Before) {
		return nil, core.ErrChanged
	}
	if preflight, supported := r.store.(interface {
		preflight(context.Context, []core.Scope) error
	}); supported {
		local := slices.Clone(scopes)
		if r.options.TestRoot == "" {
			local = slices.DeleteFunc(local, func(scope core.Scope) bool { return scope == core.System })
		}
		if err := preflight.preflight(ctx, local); err != nil {
			return nil, err
		}
	}
	var helper helperSession
	if slices.Contains(scopes, core.System) && r.options.TestRoot == "" {
		if r.start == nil {
			return nil, ErrUnauthorized
		}
		helper, err = r.start(ctx, *a)
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		if helper != nil {
			helper.close()
		}
		return nil, err
	}
	r.active = a
	r.helper = helper
	r.written = map[core.Scope]bool{}
	r.rolledBack = map[core.Scope]bool{}
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.helper != nil {
				r.helper.close()
			}
			r.helper = nil
			r.active = nil
			r.written = nil
			r.rolledBack = nil
		})
	}, nil
}
func (r *Registry) Write(ctx context.Context, scope core.Scope, value core.Value) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	a := r.active
	if a == nil || !slices.Contains(a.Scopes, scope) || r.rolledBack[scope] {
		return ErrUnauthorized
	}
	if err := value.Validate(); err != nil {
		return err
	}
	forward := !r.written[scope]
	expected, next := a.Target.Value(scope), a.Before.Value(scope)
	if forward {
		expected, next = a.Before.Value(scope), a.Target.Value(scope)
	}
	if !value.Equal(next) {
		return ErrUnauthorized
	}
	current, err := r.store.read(ctx)
	if err != nil {
		return err
	}
	// 读回与写入仍不是跨进程原子操作；每次写前核对，绝不覆盖已观察到的外部变更。
	if !current.Value(scope).Equal(expected) {
		return core.ErrChanged
	}
	if r.helper != nil && scope == core.System {
		err = r.helper.write(ctx, scope, value)
	} else {
		err = r.store.write(ctx, scope, value)
	}
	// 即使底层报告失败，core 也会读回；只允许向确认的 before 回滚，不重新尝试任意目标。
	if forward {
		r.written[scope] = true
	} else {
		r.rolledBack[scope] = true
	}
	return err
}
func (r *Registry) Notify(ctx context.Context) error { return r.store.notify(ctx) }
func validateResponseError(message string) error {
	if message == "" {
		return nil
	}
	return fmt.Errorf("受限助手: %s", message)
}
