//go:build windows

package toolnest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"toolnest/internal/app"
	"toolnest/internal/core"
	"toolnest/internal/platform/lifecycle"
)

type legacyIntegration struct {
	mu          sync.Mutex
	manager     *lifecycle.LegacyManager
	journal     *lifecycle.FileJournal
	directory   string
	run         lifecycle.RunStore
	tasks       lifecycle.TaskStore
	account     lifecycle.Account
	current     map[string]lifecycle.LegacyEntry
	executables []string
}

func newIntegrations(env environment) (*lifecycle.RunManager, *legacyIntegration, error) {
	account, err := lifecycle.CurrentAccount()
	if err != nil {
		return nil, nil, err
	}
	var run *lifecycle.WindowsRunStore
	if env.testing() {
		run, err = lifecycle.NewIsolatedWindowsRunStore(`Software\ToolNest\Tests\` + env.testRoot + `\Run`)
	} else {
		run, err = lifecycle.NewWindowsRunStore()
	}
	if err != nil {
		return nil, nil, err
	}
	startupJournal, err := lifecycle.NewFileJournal(filepath.Join(env.root, "lifecycle", "startup"))
	if err != nil {
		return nil, nil, err
	}
	startup, err := lifecycle.NewRunManager(run, startupJournal, account, env.executable)
	if err != nil {
		return nil, nil, err
	}
	legacy := &legacyIntegration{directory: filepath.Join(env.root, "lifecycle", "legacy"), run: run, account: account}
	legacy.journal, err = lifecycle.NewFileJournal(legacy.directory)
	if err != nil {
		return nil, nil, err
	}
	known := append([]string(nil), env.legacyExecutables...)
	if len(known) == 0 {
		known, err = legacy.loadPolicy()
		if err != nil {
			return nil, nil, err
		}
	}
	legacy.executables = known
	if env.testing() {
		// 隔离模式从不连接真实 Task Scheduler，即便测试名与旧入口同名。
		legacy.tasks = isolatedTasks{}
	} else {
		adapterPaths := known
		if len(adapterPaths) == 0 {
			// 此路径只用于构造只读适配器，不成为旧版归属依据。
			adapterPaths = []string{env.executable}
		}
		legacy.tasks, err = lifecycle.NewWindowsTaskStore(adapterPaths)
		if err != nil {
			return nil, nil, err
		}
	}
	if len(known) != 0 {
		policy := lifecycle.KnownLegacyPolicy(account, known)
		legacy.manager, err = lifecycle.NewLegacyManager(run, legacy.tasks, legacy.journal, policy)
		if err != nil {
			return nil, nil, err
		}
	}
	return startup, legacy, nil
}

type legacyPolicy struct {
	SID         string   `json:"sid"`
	Executables []string `json:"executables"`
}

func (legacy *legacyIntegration) policyPath() string {
	return filepath.Join(filepath.Dir(legacy.directory), "legacy-policy.json")
}

func (legacy *legacyIntegration) loadPolicy() ([]string, error) {
	file, err := os.Open(legacy.policyPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var policy legacyPolicy
	decoder := json.NewDecoder(io.LimitReader(file, 65537))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil || policy.SID != legacy.account.SID || len(policy.Executables) > 32 {
		return nil, errors.New("旧入口归属记录无效，拒绝接管")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("旧入口归属记录包含额外内容")
	}
	var checked paths
	for _, executable := range policy.Executables {
		if err := checked.Set(executable); err != nil {
			return nil, err
		}
	}
	return checked, nil
}

func (legacy *legacyIntegration) savePolicy() error {
	data, err := json.Marshal(legacyPolicy{SID: legacy.account.SID, Executables: legacy.executables})
	if err != nil {
		return err
	}
	if err := core.CheckDirectory(filepath.Dir(legacy.directory)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(legacy.directory), 0700); err != nil {
		return err
	}
	// 先持久化已经确认过的路径依据，普通重新打开也能核验 journal 并撤销。
	return core.AtomicWrite(legacy.policyPath(), data, true)
}

func (legacy *legacyIntegration) detect(ctx context.Context) ([]lifecycle.LegacyEntry, error) {
	if legacy.manager != nil {
		return legacy.manager.Detect(ctx)
	}
	var result []lifecycle.LegacyEntry
	run, err := legacy.run.ReadRun(ctx, lifecycle.LegacyRunName)
	if err != nil {
		return nil, err
	}
	if run.Exists {
		result = append(result, lifecycle.LegacyEntry{Kind: "run", Name: lifecycle.LegacyRunName, Run: run, Ownership: lifecycle.Unknown,
			Reason: "未提供已知旧版执行路径，无法确认归属，保持原状"})
	}
	name := "PathGuard-PATH-Backup-" + legacy.account.SID
	task, err := legacy.tasks.ReadTask(ctx, name)
	if err != nil {
		return result, err
	}
	if task.Exists {
		result = append(result, lifecycle.LegacyEntry{Kind: "task", Name: name, Task: task, Ownership: lifecycle.Unknown,
			Reason: "需要已知旧版完整路径并核实账号、参数和旧标记，保持原状"})
	}
	return result, nil
}

func legacyID(entry lifecycle.LegacyEntry) string { return entry.Kind + ":" + entry.Name }

func (legacy *legacyIntegration) entries() []app.LegacyEntry {
	legacy.mu.Lock()
	defer legacy.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	detected, err := legacy.detect(ctx)
	legacy.current = make(map[string]lifecycle.LegacyEntry)
	result := make([]app.LegacyEntry, 0, len(detected))
	for _, entry := range detected {
		if entry.Ownership == lifecycle.Missing {
			continue
		}
		id := legacyID(entry)
		legacy.current[id] = entry
		result = append(result, app.LegacyEntry{ID: id, Kind: entry.Kind, Description: entry.Name + " · " + entry.Reason,
			Owned: entry.Ownership == lifecycle.Confirmed})
	}
	if err != nil {
		result = append(result, app.LegacyEntry{ID: "detection-error", Description: "旧入口检查失败，未修改系统：" + err.Error()})
	}
	backups, err := os.ReadDir(legacy.directory)
	if errors.Is(err, os.ErrNotExist) {
		return result
	}
	if err != nil {
		return append(result, app.LegacyEntry{ID: "journal-error", Description: "旧入口撤销记录读取失败：" + err.Error()})
	}
	// 持久 journal 本身就是恢复索引；重启后不依赖内存 map，也不删除审计副本。
	for _, file := range backups {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(file.Name(), ".json")
		record, err := legacy.journal.Load(ctx, id)
		if err != nil || record.Account.SID != legacy.account.SID {
			continue
		}
		changed, checkErr := legacy.canUndo(ctx, record)
		if checkErr != nil || !changed {
			continue
		}
		result = append(result, app.LegacyEntry{ID: "undo:" + id, Kind: record.Kind, Owned: legacy.manager != nil, Disabled: true,
			Description: record.Name + " · 已停用，保留可撤销记录 " + id})
	}
	return result
}

func (legacy *legacyIntegration) canUndo(ctx context.Context, record lifecycle.UndoRecord) (bool, error) {
	if record.Kind == "run" && record.Name == lifecycle.LegacyRunName && record.BeforeRun.Exists && !record.AfterRun.Exists {
		value, err := legacy.run.ReadRun(ctx, record.Name)
		return value.Equal(record.AfterRun), err
	}
	if record.Kind == "task" && record.Name == "PathGuard-PATH-Backup-"+legacy.account.SID && record.BeforeTask.Enabled && !record.AfterTask.Enabled {
		value, err := legacy.tasks.ReadTask(ctx, record.Name)
		// 最终 XML 和归属由 LegacyManager.Undo 再次严格核对；这里仅决定入口可见性。
		return value.Exists && !value.Enabled, err
	}
	return false, nil
}

func (legacy *legacyIntegration) disable(id string) error {
	legacy.mu.Lock()
	defer legacy.mu.Unlock()
	if legacy.manager == nil {
		return lifecycle.ErrNotOwned
	}
	entry, exists := legacy.current[id]
	if !exists || entry.Ownership != lifecycle.Confirmed {
		return lifecycle.ErrNotOwned
	}
	if err := legacy.savePolicy(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	change, err := legacy.manager.Disable(ctx, entry)
	if err != nil && change.BackupID != "" {
		return fmt.Errorf("旧入口未完成停用，撤销记录 %s 已保留: %w", change.BackupID, err)
	}
	return err
}

func (legacy *legacyIntegration) undo(id string) error {
	legacy.mu.Lock()
	defer legacy.mu.Unlock()
	if legacy.manager == nil || !strings.HasPrefix(id, "undo:") {
		return lifecycle.ErrNotOwned
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	record, err := legacy.journal.Load(ctx, strings.TrimPrefix(id, "undo:"))
	if err != nil {
		return err
	}
	_, err = legacy.manager.Undo(ctx, record)
	return err
}

type isolatedTasks struct{}

func (isolatedTasks) ReadTask(context.Context, string) (lifecycle.TaskValue, error) {
	return lifecycle.TaskValue{}, nil
}

func (isolatedTasks) SetTaskEnabled(context.Context, string, lifecycle.TaskValue, bool) (lifecycle.TaskValue, error) {
	return lifecycle.TaskValue{}, errors.New("隔离入口不接触真实计划任务")
}
