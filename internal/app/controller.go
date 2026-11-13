package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"toolnest/internal/core"
	"toolnest/internal/network"
	"toolnest/internal/update"
)

type Hooks struct {
	Copy            func(string) error
	ChooseDirectory func() (string, error)
	OpenDirectory   func(string) error
	OpenURL         func(string) error
	SetStartup      func(bool) error
	Startup         func() bool
	Theme           func(string)
	Hide            func() error
	Quit            func()
	Notify          func(string, string) error
	DisableLegacy   func(string) error
	UndoLegacy      func(string) error
	LegacyEntries   func() []LegacyEntry
	RestartUpdate   func() error
}

type Options struct {
	Root, Version                     string
	Registry                          core.Registry
	Vault                             network.Vault
	Hooks                             Hooks
	Source                            update.Source
	PublicKey, InstallDir, Executable string
	UpdateEndpoint                    string
	ReleasePage                       string
}

type Controller struct {
	mu               sync.RWMutex
	root             string
	version          string
	config           config
	service          *core.Service
	view             View
	plans            map[string]core.RestorePlan
	hooks            Hooks
	network          *network.Manager
	vault            network.Vault
	updater          *update.Manager
	ctx              context.Context
	cancel           context.CancelFunc
	gate             sync.Mutex
	closed           bool
	lastNotification string
	updateEndpoint   string
	updateConfig     update.Config
	automaticError   string
	refreshError     string
	exitGeneration   uint64
	releasePage      string
}

func New(options Options) (*Controller, error) {
	if !filepath.IsAbs(options.Root) || options.Registry == nil {
		return nil, errors.New("需要明确的数据目录和注册表适配器")
	}
	if err := core.CheckDirectory(options.Root); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(options.Root, 0700); err != nil {
		return nil, err
	}
	configuration, configErr := loadConfig(options.Root)
	ctx, cancel := context.WithCancel(context.Background())
	controller := &Controller{root: options.Root, version: options.Version, config: configuration, plans: map[string]core.RestorePlan{}, hooks: options.Hooks, vault: options.Vault, ctx: ctx, cancel: cancel}
	controller.updateEndpoint = options.UpdateEndpoint
	if strings.HasPrefix(options.ReleasePage, "https://github.com/") {
		controller.releasePage = options.ReleasePage
	}
	controller.service = &core.Service{Registry: options.Registry, Store: core.Store{Directory: configuration.Settings.SnapshotDir}, Now: time.Now}
	_, currentErr := os.Stat(filepath.Join(options.Root, "settings.json"))
	_, previousErr := os.Stat(filepath.Join(options.Root, "settings.json.previous"))
	if errors.Is(currentErr, os.ErrNotExist) && errors.Is(previousErr, os.ErrNotExist) && configErr == nil {
		if err := controller.service.Store.Ensure(); err != nil {
			cancel()
			return nil, err
		}
	}
	if options.Hooks.Startup != nil {
		controller.config.Settings.LaunchAtLogin = options.Hooks.Startup()
	}
	manager, err := network.New(ctx, controller.networkConfig(), options.Vault)
	if err != nil {
		// 公共网络不可用仍保留离线 PATH 功能；联网入口必须明确拒绝，不能回退直连。
		configErr = errors.Join(configErr, fmt.Errorf("公共代理配置不可用，请修复凭据或配置: %w", err))
		manager = nil
	}
	controller.network = manager
	updateConfig := update.Config{CurrentVersion: options.Version, Source: options.Source, PublicKey: options.PublicKey, InstallDir: options.InstallDir, StateDir: filepath.Join(options.Root, "updates"), Executable: options.Executable, Network: manager, Gate: controller}
	if manager == nil {
		updateConfig.Source = nil
	}
	controller.updater, err = update.New(updateConfig)
	if err != nil {
		cancel()
		return nil, err
	}
	controller.updateConfig = updateConfig
	controller.updateConfig.Source = options.Source
	controller.view = View{Version: options.Version, Settings: controller.config.Settings, Status: "就绪"}
	controller.loadRecords()
	if configErr != nil {
		controller.view.Error = configErr.Error()
	}
	controller.refresh(context.Background())
	return controller, nil
}

func (controller *Controller) networkConfig() network.Config {
	settings := controller.config.Settings
	configuration := network.Config{Mode: network.Mode(settings.ProxyMode)}
	if settings.ProxyMode == "custom" {
		configuration.Address = settings.ProxyURL
		configuration.CredentialID = controller.config.CredentialID
	}
	return configuration
}

func (controller *Controller) TryAcquire(operation string) (func(), error) {
	if !controller.gate.TryLock() {
		return nil, core.ErrBusy
	}
	controller.mu.Lock()
	if controller.closed || controller.view.PendingExit != "" && operation != "安全退出" {
		controller.mu.Unlock()
		controller.gate.Unlock()
		return nil, errors.New("应用正在退出")
	}
	controller.view.Busy = operation
	controller.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			controller.mu.Lock()
			controller.view.Busy = ""
			controller.mu.Unlock()
			controller.gate.Unlock()
		})
	}, nil
}

func (controller *Controller) Start() {
	go func() {
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-controller.ctx.Done():
				return
			case <-timer.C:
				controller.checkUpdate(true)
				timer.Reset(24 * time.Hour)
			}
		}
	}()
	go func() {
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-controller.ctx.Done():
				return
			case <-timer.C:
				controller.tick(time.Now())
			}
		}
	}()
}

func (controller *Controller) View() View {
	controller.mu.RLock()
	result := controller.view
	result.Settings = controller.config.Settings
	result.Settings.ProxyPassword = ""
	result.Records = slices.Clone(controller.view.Records)
	result.Snapshots = slices.Clone(controller.view.Snapshots)
	result.Result = cloneResult(controller.view.Result)
	result.Problems = slices.Clone(controller.view.Problems)
	for index := range result.Records {
		if result.Records[index].Result != nil {
			detail := cloneResult(*result.Records[index].Result)
			result.Records[index].Result = &detail
		}
	}
	result.User.Entries = slices.Clone(result.User.Entries)
	result.System.Entries = slices.Clone(result.System.Entries)
	for index := range result.Snapshots {
		result.Snapshots[index].User.Entries = slices.Clone(result.Snapshots[index].User.Entries)
		result.Snapshots[index].System.Entries = slices.Clone(result.Snapshots[index].System.Entries)
	}
	result.LegacyEntries = slices.Clone(controller.view.LegacyEntries)
	result.NextRun = controller.config.Schedule.Description()
	result.LastPlanResult = controller.config.Schedule.LastResult
	if !controller.config.Schedule.RetryAt.IsZero() {
		result.RetryAt = controller.config.Schedule.RetryAt.Local().Format("2006-01-02 15:04")
	}
	var activeErrors []string
	for _, message := range []string{result.Error, controller.automaticError, controller.refreshError} {
		if message != "" && !slices.Contains(activeErrors, message) {
			activeErrors = append(activeErrors, message)
		}
	}
	result.Error = strings.Join(activeErrors, "\n")
	manager := controller.updater
	controller.mu.RUnlock()
	status := manager.Status()
	result.Update = Update{State: string(status.Phase), Current: status.CurrentVersion, Available: status.AvailableVersion, Notes: status.Notes,
		Error: updateFailureText(status.FailureCode, status.Failure), ErrorCode: status.FailureCode, NetworkPhase: string(status.NetworkPhase),
		Downloaded: status.Downloaded, Total: status.Total, BytesPerSecond: status.BytesPerSecond, RecoveryPath: status.RecoveryPath,
		DismissedVersion: status.DismissedVersion, NewerVersion: status.NewerVersion, Details: status.Failure, SourceURL: controller.releasePage, RolledBack: status.RolledBack}
	if !status.PreparedAt.IsZero() {
		result.Update.PreparedAt = status.PreparedAt.Local().Format("2006-01-02 15:04")
	}
	if !status.LastCheck.IsZero() {
		result.Update.LastCheck = status.LastCheck.Local().Format("2006-01-02 15:04")
	}
	if status.Total > 0 {
		result.Update.Progress = float64(status.Downloaded) / float64(status.Total)
	}
	return result
}

func toScope(value core.Value) Scope {
	result := Scope{Exists: value.Exists, Type: value.TypeName(), Text: value.Text()}
	result.RawDigest = fmt.Sprintf("%x", sha256.Sum256(value.Bytes))
	result.RawSize = len(value.Bytes)
	for _, entry := range core.Filter(value, "") {
		result.Entries = append(result.Entries, Entry{entry.Index, entry.Text})
	}
	return result
}

func (controller *Controller) refresh(ctx context.Context) error {
	state, readErr := controller.service.Registry.Read(ctx)
	listing, listErr := controller.service.Store.List()
	var snapshots []Snapshot
	for _, snapshot := range listing.Snapshots {
		summary := fmt.Sprintf("用户 %d 项 · 系统 %d 项", len(snapshot.User.Entries()), len(snapshot.System.Entries()))
		if snapshot.Origin != "" {
			summary += " · 旧版导入"
		}
		snapshots = append(snapshots, Snapshot{ID: snapshot.ID, CreatedAt: snapshot.CreatedAt.Local().Format("2006-01-02 15:04:05"), Source: snapshot.Source, Summary: summary, User: toScope(snapshot.User), System: toScope(snapshot.System)})
	}
	for _, invalid := range listing.Invalid {
		snapshots = append(snapshots, Snapshot{ID: invalid.Name, Error: invalid.Error, Summary: "快照不可用"})
	}
	legacyCount := 0
	if local := os.Getenv("LOCALAPPDATA"); filepath.IsAbs(local) {
		if old, err := (core.Store{Directory: filepath.Join(local, "PathGuard", "snapshots")}).List(); err == nil {
			legacyCount = len(old.Snapshots)
		}
	}
	var legacy []LegacyEntry
	if controller.hooks.LegacyEntries != nil {
		legacy = controller.hooks.LegacyEntries()
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if readErr == nil {
		controller.view.User, controller.view.System = toScope(state.User), toScope(state.System)
	}
	if listErr == nil {
		controller.view.Snapshots = snapshots
	}
	controller.view.LegacyCount, controller.view.LegacyEntries = legacyCount, legacy
	controller.view.Problems = nil
	if listErr != nil {
		controller.view.Problems = append(controller.view.Problems, directoryProblem(listErr))
	}
	if readErr != nil {
		controller.view.Problems = append(controller.view.Problems, Problem{Code: "path-read", Title: "PATH 暂时无法读取",
			Message: "本次没有修改 PATH。请重试读取；持续失败时查看详细原因。", Details: readErr.Error(), Action: "refresh"})
	}
	if err := errors.Join(readErr, listErr); err != nil {
		controller.refreshError = err.Error()
		return err
	}
	controller.refreshError = ""
	return nil
}

func (controller *Controller) run(operation string, execute func(context.Context) (string, string, error)) {
	release, err := controller.TryAcquire(operation)
	if err != nil {
		controller.setError(err)
		return
	}
	go func() {
		defer release()
		started := time.Now()
		controller.mu.Lock()
		controller.view.Error = ""
		controller.view.Status = ""
		if operation == "手动备份" || operation == "恢复 PATH" || operation == "首次设置" {
			controller.view.Result = OperationResult{}
		}
		controller.mu.Unlock()
		outcome, snapshotID, err := execute(controller.ctx)
		if outcome == "" {
			outcome = "success"
			if err != nil {
				outcome = "failed"
			}
		}
		controller.mu.Lock()
		if outcome == "cancelled" {
			controller.view.Error = ""
			controller.view.Status = operation + "已取消"
		} else if err != nil {
			controller.view.Error = err.Error()
			controller.view.Status = operation + "未完成"
		} else {
			if controller.view.Status == "" {
				switch outcome {
				case "cancelled":
					controller.view.Status = operation + "已取消"
				case "unchanged":
					controller.view.Status = operation + "无变化"
				case "partial":
					controller.view.Status = operation + "部分完成"
				default:
					controller.view.Status = operation + "完成"
				}
			}
		}
		var completed *OperationResult
		if operation == "手动备份" || operation == "恢复 PATH" || operation == "首次设置" {
			if controller.view.Result.ID == "" && err != nil {
				controller.view.Result = OperationResult{ID: core.NewID(), Operation: operation, Outcome: outcome, Title: operation + "未完成",
					Message: "本次操作没有完成，请查看原因后重试。", Details: err.Error()}
			}
			if controller.view.Result.ID != "" {
				detail := cloneResult(controller.view.Result)
				completed = &detail
			}
		}
		controller.mu.Unlock()
		message := errorText(err)
		if completed != nil {
			message = completed.Title + "：" + completed.Message
		}
		if message == "" {
			message = operation + "完成"
			if outcome == "cancelled" {
				message = operation + "已取消"
			}
		}
		controller.record(Record{At: started.Local().Format(time.RFC3339), FinishedAt: time.Now().Local().Format(time.RFC3339), Operation: operation, Trigger: "manual", Outcome: outcome, SnapshotID: snapshotID, Message: message, Result: completed})
		controller.refresh(context.Background())
	}()
}

func errorText(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
func (controller *Controller) setError(err error) {
	controller.mu.Lock()
	controller.view.Error = errorText(err)
	controller.mu.Unlock()
}

func (controller *Controller) Refresh() {
	controller.run("刷新", func(ctx context.Context) (string, string, error) { return "", "", controller.refresh(ctx) })
}
func (controller *Controller) Backup() {
	controller.run("手动备份", func(ctx context.Context) (string, string, error) {
		snapshot, _, err := controller.service.Backup(ctx, "manual")
		controller.setResult(backupResult(snapshot, err))
		return "", snapshot.ID, err
	})
}

func (controller *Controller) PreviewRestore(id string, user, system bool) (Preview, error) {
	var scopes []core.Scope
	if user {
		scopes = append(scopes, core.User)
	}
	if system {
		scopes = append(scopes, core.System)
	}
	release, err := controller.TryAcquire("恢复预览")
	if err != nil {
		return Preview{}, err
	}
	defer release()
	plan, err := controller.service.Preview(controller.ctx, id, scopes)
	if err != nil {
		return Preview{}, err
	}
	controller.mu.Lock()
	controller.plans = map[string]core.RestorePlan{plan.Token: plan}
	controller.mu.Unlock()
	preview := Preview{Token: plan.Token, SnapshotID: id, User: user, System: system}
	for _, change := range plan.Changes {
		preview.Changes = append(preview.Changes, Change{change.Kind, string(change.Scope), change.Before, change.After})
	}
	return preview, nil
}

func (controller *Controller) Restore(preview Preview) {
	controller.run("恢复 PATH", func(ctx context.Context) (string, string, error) {
		controller.mu.Lock()
		plan, ok := controller.plans[preview.Token]
		delete(controller.plans, preview.Token)
		controller.mu.Unlock()
		if !ok {
			return "failed", "", errors.New("恢复预览已失效，请重新确认")
		}
		if confirmed, supported := controller.service.Registry.(interface{ ConfirmRestore(core.RestorePlan) error }); supported {
			if err := confirmed.ConfirmRestore(plan); err != nil {
				return "failed", "", err
			}
		}
		result := controller.service.Restore(ctx, plan)
		controller.setResult(restoreResult(plan, result))
		if result.NotificationError != nil {
			controller.mu.Lock()
			controller.view.Status = "PATH 已恢复，但环境通知失败；请重新打开终端"
			controller.mu.Unlock()
		}
		return result.Outcome, result.ProtectionID, result.Error
	})
}

func (controller *Controller) DeleteSnapshots(ids []string) {
	selected := slices.Clone(ids)
	controller.run("清理快照", func(context.Context) (string, string, error) {
		if len(selected) == 0 {
			return "cancelled", "", nil
		}
		for _, id := range selected {
			if _, err := controller.service.Store.Load(id); err != nil {
				return "failed", "", err
			}
		}
		var failures []error
		for _, id := range selected {
			if err := os.Remove(filepath.Join(controller.service.Store.Directory, id+".json")); err != nil {
				failures = append(failures, err)
			}
		}
		if len(failures) > 0 {
			return "partial", "", errors.Join(failures...)
		}
		return "success", "", nil
	})
}

func (controller *Controller) loadRecords() {
	data, err := os.ReadFile(filepath.Join(controller.root, "records.json"))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil || len(data) > 64<<20 {
		controller.view.Error = "执行记录读取失败"
		return
	}
	if err = json.Unmarshal(data, &controller.view.Records); err != nil {
		controller.view.Error = "执行记录格式无效"
	}
}

func (controller *Controller) record(record Record) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.view.Records = append(controller.view.Records, record)
	data, err := json.MarshalIndent(controller.view.Records, "", "  ")
	if err == nil {
		err = core.AtomicWrite(filepath.Join(controller.root, "records.json"), data, true)
	}
	if err != nil {
		controller.view.Error = "执行记录持久保存失败: " + err.Error()
	}
}

func (controller *Controller) SetTrayAvailable(available bool) {
	controller.mu.Lock()
	controller.view.TrayAvailable = available
	controller.mu.Unlock()
}
func (controller *Controller) Updater() *update.Manager {
	controller.mu.RLock()
	defer controller.mu.RUnlock()
	return controller.updater
}
func (controller *Controller) UpdateEndpoint() string { return controller.updateEndpoint }
func (controller *Controller) SetHooks(hooks Hooks) {
	controller.mu.Lock()
	controller.hooks = hooks
	controller.mu.Unlock()
}
