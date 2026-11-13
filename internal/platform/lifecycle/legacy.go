package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

// TaskValue includes full XML for preview, change detection and durable undo.
// Scheduler backends may only toggle Enabled on an otherwise byte-identical definition.
type TaskValue struct {
	Exists      bool   `json:"exists"`
	Name        string `json:"name"`
	XML         string `json:"xml"`
	Enabled     bool   `json:"enabled"`
	UserID      string `json:"userID"`
	Author      string `json:"author"`
	Source      string `json:"source"`
	Description string `json:"description"`
	Executable  string `json:"executable"`
	Arguments   string `json:"arguments"`
	ActionCount int    `json:"actionCount"`
	ActionType  int    `json:"actionType"`
	LogonType   int    `json:"logonType"`
	RunLevel    int    `json:"runLevel"`
}

func (t TaskValue) Equal(o TaskValue) bool { return t == o }

// TaskStore only addresses a known current-user PathGuard task, never enumerates all tasks.
type TaskStore interface {
	ReadTask(context.Context, string) (TaskValue, error)
	SetTaskEnabled(context.Context, string, TaskValue, bool) (TaskValue, error)
}

// LegacyPolicy must come from a known prior installation, not executable names guessed from the disk.
// RunNames accepts only PathGuard; TaskNames accepts only PathGuard-PATH-Backup-<current SID>.
type LegacyPolicy struct {
	Account     Account
	Executables []string
	RunNames    []string
	TaskNames   []string
}

func KnownLegacyPolicy(account Account, executables []string) LegacyPolicy {
	return LegacyPolicy{account, append([]string(nil), executables...), []string{LegacyRunName}, []string{"PathGuard-PATH-Backup-" + account.SID}}
}

type Ownership string

const (
	Missing   Ownership = "missing"
	Confirmed Ownership = "confirmed"
	Unknown   Ownership = "unknown"
)

type LegacyEntry struct {
	Kind      string
	Name      string
	Ownership Ownership
	Reason    string
	Run       RunValue
	Task      TaskValue
}

type LegacyManager struct {
	run     RunStore
	tasks   TaskStore
	journal Journal
	policy  LegacyPolicy
}

func NewLegacyManager(run RunStore, tasks TaskStore, journal Journal, p LegacyPolicy) (*LegacyManager, error) {
	if run == nil || tasks == nil || journal == nil || p.Account.SID == "" || len(p.Executables) == 0 {
		return nil, errors.New("legacy stores, journal, account and explicit old executable paths required")
	}
	for _, e := range p.Executables {
		if err := validateExe(e); err != nil {
			return nil, err
		}
	}
	for _, n := range p.RunNames {
		if n != LegacyRunName {
			return nil, errors.New("unknown legacy Run name")
		}
	}
	for _, n := range p.TaskNames {
		if n != "PathGuard-PATH-Backup-"+p.Account.SID {
			return nil, errors.New("unknown legacy task name")
		}
	}
	p.Executables = append([]string(nil), p.Executables...)
	p.RunNames = append([]string(nil), p.RunNames...)
	p.TaskNames = append([]string(nil), p.TaskNames...)
	return &LegacyManager{run, tasks, journal, p}, nil
}
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
func (m *LegacyManager) ownsRun(name string, v RunValue) bool {
	if !contains(m.policy.RunNames, name) {
		return false
	}
	for _, exe := range m.policy.Executables {
		if (Command{exe, []string{AutostartArgument}}).owns(v) {
			return true
		}
	}
	return false
}
func (m *LegacyManager) ownsTask(name string, t TaskValue) bool {
	a := m.policy.Account
	if !contains(m.policy.TaskNames, name) || !t.Exists || t.Name != name || t.XML == "" || t.Source != LegacyTaskMarker || t.Description != LegacyTaskMarker || t.Author != "PathGuard:"+a.SID || !(strings.EqualFold(t.UserID, a.SID) || (a.Name != "" && strings.EqualFold(t.UserID, a.Name))) || t.ActionCount != 1 || t.ActionType != 0 || t.Arguments != "--backup" || t.LogonType != 3 || t.RunLevel != 0 {
		return false
	}
	if _, err := stableTaskXML(t.XML); err != nil {
		return false
	}
	for _, exe := range m.policy.Executables {
		if samePath(t.Executable, exe) {
			return true
		}
	}
	return false
}

// Detect only reads the explicitly listed names. Unknown entries are returned, never changed.
func (m *LegacyManager) Detect(ctx context.Context) ([]LegacyEntry, error) {
	var entries []LegacyEntry
	for _, n := range m.policy.RunNames {
		v, e := m.run.ReadRun(ctx, n)
		if e != nil {
			return entries, e
		}
		o := Unknown
		if !v.Exists {
			o = Missing
		} else if m.ownsRun(n, v) {
			o = Confirmed
		}
		entries = append(entries, LegacyEntry{Kind: "run", Name: n, Ownership: o, Reason: ownershipReason(o), Run: v.clone()})
	}
	for _, n := range m.policy.TaskNames {
		t, e := m.tasks.ReadTask(ctx, n)
		if e != nil {
			return entries, e
		}
		o := Unknown
		if !t.Exists {
			o = Missing
		} else if m.ownsTask(n, t) {
			o = Confirmed
		}
		entries = append(entries, LegacyEntry{Kind: "task", Name: n, Ownership: o, Reason: ownershipReason(o), Task: t})
	}
	return entries, nil
}
func ownershipReason(o Ownership) string {
	if o == Unknown {
		return "账号、执行路径、参数、类型或旧版标记不符；保持原状"
	}
	return string(o)
}

// Disable requires a user-confirmed Detect result; caller must obtain explicit confirmation.
// It does not import snapshots, enable new schedules, delete tasks or stop processes.
func (m *LegacyManager) Disable(ctx context.Context, e LegacyEntry) (Change, error) {
	if e.Ownership != Confirmed {
		return Change{}, ErrNotOwned
	}
	switch e.Kind {
	case "run":
		if !m.ownsRun(e.Name, e.Run) {
			return Change{}, ErrNotOwned
		}
		return changeRun(ctx, m.run, m.journal, m.policy.Account, e.Name, e.Run, RunValue{})
	case "task":
		if !m.ownsTask(e.Name, e.Task) {
			return Change{}, ErrNotOwned
		}
		if !e.Task.Enabled {
			return Change{}, nil
		}
		return m.changeTask(ctx, e.Name, e.Task, false)
	default:
		return Change{}, ErrNotOwned
	}
}
func (m *LegacyManager) changeTask(ctx context.Context, name string, before TaskValue, enabled bool) (Change, error) {
	now, err := m.tasks.ReadTask(ctx, name)
	if err != nil {
		return Change{}, err
	}
	if !now.Equal(before) || !m.ownsTask(name, now) {
		return Change{}, ErrConflict
	}
	record := UndoRecord{Version: 1, Account: m.policy.Account, Kind: "task", Name: name, BeforeTask: before}
	record.AfterTask = before
	record.AfterTask.Enabled = enabled
	// 完整 XML 必须先持久备份，后端还会在真正切换前重复校验 XML 与 Enabled。
	id, err := m.journal.Save(ctx, record)
	result := Change{BackupID: id, Record: record}
	if err != nil {
		return result, fmt.Errorf("save undo backup: %w", err)
	}
	after, err := m.tasks.SetTaskEnabled(ctx, name, before, enabled)
	if err != nil {
		return result, err
	}
	result.Record.AfterTask = after
	if !m.ownsTask(name, after) || after.Enabled != enabled || !sameTaskDefinition(before, after) {
		return result, errors.New("task changed unexpectedly after toggle; undo backup retained")
	}
	result.Changed = true
	return result, nil
}

func (m *LegacyManager) Undo(ctx context.Context, r UndoRecord) (Change, error) {
	if r.Version != 1 || r.Account.SID != m.policy.Account.SID {
		return Change{}, ErrNotOwned
	}
	switch r.Kind {
	case "run":
		if !m.ownsRun(r.Name, r.BeforeRun) || r.AfterRun.Exists || len(r.AfterRun.Data) != 0 || r.AfterRun.Type != 0 {
			return Change{}, ErrNotOwned
		}
		return changeRun(ctx, m.run, m.journal, m.policy.Account, r.Name, r.AfterRun, r.BeforeRun)
	case "task":
		if !m.ownsTask(r.Name, r.BeforeTask) || !m.ownsTask(r.Name, r.AfterTask) || !r.BeforeTask.Enabled || r.AfterTask.Enabled || !sameTaskDefinition(r.BeforeTask, r.AfterTask) {
			return Change{}, ErrNotOwned
		}
		// 持久备份写于禁用之前；COM 可能规范化 Enabled 文本，重读后只接受该字段差异。
		now, err := m.tasks.ReadTask(ctx, r.Name)
		if err != nil {
			return Change{}, err
		}
		if !m.ownsTask(r.Name, now) || now.Enabled != r.AfterTask.Enabled || !sameTaskDefinition(now, r.AfterTask) {
			return Change{}, ErrConflict
		}
		return m.changeTask(ctx, r.Name, now, true)
	default:
		return Change{}, ErrNotOwned
	}
}

// cloneRecord prevents a fake or async journal from sharing mutable registry byte buffers.
func cloneRecord(r UndoRecord) UndoRecord {
	r.BeforeRun.Data = bytes.Clone(r.BeforeRun.Data)
	r.AfterRun.Data = bytes.Clone(r.AfterRun.Data)
	return r
}
