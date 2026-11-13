// Package update separates discovery, verified staging and explicit activation.
package update

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"toolnest/internal/network"
)

var (
	ErrNotConfigured = errors.New("update source and production public key are not configured")
	ErrBusy          = errors.New("update or application write is busy")
	ErrSignature     = errors.New("update signature is invalid")
	ErrSize          = errors.New("update size limit or expected size mismatch")
	ErrArchive       = errors.New("unsafe or incompatible update archive")
	ErrVersion       = errors.New("invalid, unstable or mismatched release version")
	ErrBlocked       = errors.New("release blocked after an unsuccessful startup; manual retry required")
	ErrNotReady      = errors.New("no verified update is ready")
	ErrRecovery      = errors.New("update recovery failed; retained files require manual recovery")
	ErrStartup       = errors.New("updated application did not confirm startup")
)

type Phase string

const (
	Unconfigured        Phase = "unconfigured"
	Unchecked           Phase = "unchecked"
	Checking            Phase = "checking"
	Latest              Phase = "latest"
	Available           Phase = "available"
	Downloading         Phase = "downloading"
	Verifying           Phase = "verifying"
	Ready               Phase = "ready"
	Waiting             Phase = "waiting-safe-switch"
	Applying            Phase = "applying"
	PendingConfirmation Phase = "pending-startup-confirmation"
	Confirmed           Phase = "confirmed"
	Blocked             Phase = "blocked"
	Failed              Phase = "failed"
)

type Status struct {
	Phase                                   Phase
	CurrentVersion, AvailableVersion, Notes string
	LastCheck                               time.Time
	Downloaded, Total                       int64
	BytesPerSecond                          float64
	Failure                                 string
	UsingDelta                              bool
	FallbackReason                          string
	NetworkPhase                            Phase
	FailureCode                             string
	PreparedAt                              time.Time
	NewerVersion, DismissedVersion          string
	RecoveryPath                            string
	RolledBack                              bool
}

// Gate must be the SAME exclusive coordinator used by backup/restore/import.
// A check-only Busy callback is insufficient: it races the subsequent rename.
type Gate interface {
	TryAcquire(operation string) (release func(), err error)
}

type LocalGate struct {
	mu   sync.Mutex
	busy bool
}

func (gate *LocalGate) TryAcquire(_ string) (func(), error) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.busy {
		return nil, ErrBusy
	}
	gate.busy = true
	var once sync.Once
	return func() { once.Do(func() { gate.mu.Lock(); gate.busy = false; gate.mu.Unlock() }) }, nil
}

// DeltaApplier is optional. Without a compatible MyGo adapter the full package
// is used; callers never need deltas for a reliable update.
type DeltaApplier func(ctx context.Context, deltaFile, oldExecutable, destination, fromVersion, toVersion string) error

type Config struct {
	CurrentVersion string
	Source         Source
	PublicKey      string
	InstallDir     string
	StateDir       string
	Executable     string
	Network        *network.Manager
	Gate           Gate
	MaxDownload    int64
	MaxExecutable  int64
	ApplyDelta     DeltaApplier
}

type Manager struct {
	mu              sync.Mutex
	config          Config
	key             ed25519.PublicKey
	status          Status
	working         bool
	release         *Release
	prepared        *Prepared
	manualRetry     bool
	networkChanging bool
	requestCancel   context.CancelFunc
	rename          func(string, string) error
	presentation    presentation
}

func New(config Config) (*Manager, error) {
	if _, err := stableVersion(config.CurrentVersion); err != nil {
		return nil, err
	}
	if config.Executable == "" {
		config.Executable = "ToolNest.exe"
	}
	if !validFilename(config.Executable) || !strings.EqualFold(filepath.Ext(config.Executable), ".exe") {
		return nil, ErrArchive
	}
	if config.MaxDownload == 0 {
		config.MaxDownload = 256 << 20
	}
	if config.MaxExecutable == 0 {
		config.MaxExecutable = 512 << 20
	}
	if config.MaxDownload < 1 || config.MaxDownload > 1<<30 || config.MaxExecutable < 1 || config.MaxExecutable > 1<<30 {
		return nil, ErrSize
	}
	manager := &Manager{config: config, status: Status{Phase: Unchecked, CurrentVersion: config.CurrentVersion}, rename: renameFile}
	if config.Source == nil || strings.TrimSpace(config.PublicKey) == "" {
		manager.status.Phase = Unconfigured
		manager.status.Failure = ErrNotConfigured.Error()
		manager.status.FailureCode = "unconfigured"
		return manager, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(config.PublicKey))
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, ErrSignature
	}
	manager.key = append(ed25519.PublicKey(nil), decoded...)
	if config.Network == nil || config.Gate == nil || !filepath.IsAbs(config.InstallDir) || !filepath.IsAbs(config.StateDir) {
		return nil, errors.New("updates require a shared network, exclusive gate and absolute install/state directories")
	}
	if strings.EqualFold(filepath.Clean(config.InstallDir), filepath.Clean(config.StateDir)) {
		return nil, errors.New("update state must be separate from the program directory")
	}
	manager.loadPresentation()
	if err := manager.loadPrepared(); err != nil {
		manager.status.Phase = Failed
		manager.status.Failure = err.Error()
		manager.status.FailureCode = failureCode(err)
	}
	return manager, nil
}

func (manager *Manager) Status() Status {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.status
}
func (manager *Manager) Prepared() *Prepared {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.prepared == nil {
		return nil
	}
	copy := *manager.prepared
	return &copy
}
func (manager *Manager) set(fn func(*Status)) {
	manager.mu.Lock()
	fn(&manager.status)
	manager.mu.Unlock()
}
func (manager *Manager) begin(phase Phase) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.key) == 0 {
		return ErrNotConfigured
	}
	if manager.working || manager.networkChanging {
		return ErrBusy
	}
	manager.working = true
	manager.status.Phase = phase
	manager.status.Failure = ""
	manager.status.FailureCode = ""
	return nil
}
func (manager *Manager) end(err error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.working = false
	networkOperation := manager.requestCancel != nil
	if manager.requestCancel != nil {
		manager.requestCancel()
		manager.requestCancel = nil
	}
	manager.status.NetworkPhase = ""
	if networkOperation && manager.prepared != nil {
		manager.readyLocked()
		if err != nil && !errors.Is(err, context.Canceled) {
			manager.status.Failure, manager.status.FailureCode = err.Error(), failureCode(err)
		}
		return
	}
	if errors.Is(err, context.Canceled) {
		manager.status.Failure = ""
		manager.status.FailureCode = ""
		manager.status.Phase = Unchecked
		if manager.release != nil {
			manager.status.Phase = Available
		}
		return
	}
	if err != nil {
		manager.status.Phase = Failed
		manager.status.Failure = err.Error()
		manager.status.FailureCode = failureCode(err)
		if errors.Is(err, ErrBlocked) {
			manager.status.Phase = Blocked
		}
		if errors.Is(err, ErrBusy) {
			manager.status.Phase = Waiting
		}
	}
}

func (manager *Manager) beginNetwork(ctx context.Context, phase Phase) (context.Context, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.key) == 0 {
		return ctx, ErrNotConfigured
	}
	if manager.working || manager.networkChanging {
		return ctx, ErrBusy
	}
	request, cancel := context.WithCancel(ctx)
	manager.working = true
	manager.requestCancel = cancel
	manager.status.Phase = phase
	manager.status.NetworkPhase = phase
	if manager.prepared != nil {
		manager.status.Phase = Ready
	}
	manager.status.Failure = ""
	manager.status.FailureCode = ""
	return request, nil
}

// CancelNetwork 只取消检查和下载，已开始的程序替换不能伪装成可瞬时撤销。
func (manager *Manager) CancelNetwork() bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.requestCancel == nil {
		return false
	}
	manager.requestCancel()
	return true
}

// HoldNetworkPolicy 与请求开始共用锁，避免保存代理时刚好启动另一套下载策略。
func (manager *Manager) HoldNetworkPolicy() (func(), error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.working || manager.networkChanging {
		return nil, ErrBusy
	}
	manager.networkChanging = true
	var once sync.Once
	return func() { once.Do(func() { manager.mu.Lock(); manager.networkChanging = false; manager.mu.Unlock() }) }, nil
}

func (manager *Manager) Check(ctx context.Context, manual bool) (release *Release, err error) {
	if ctx, err = manager.beginNetwork(ctx, Checking); err != nil {
		return nil, err
	}
	defer func() { manager.end(err) }()
	// 一次检查固定一个网络快照；说明、元数据和重定向不混用新旧策略。
	client := manager.config.Network.Snapshot()
	release, err = manager.config.Source.latest(ctx, client)
	manager.mu.Lock()
	manager.status.LastCheck = time.Now()
	manager.presentation.LastCheck = manager.status.LastCheck
	persistErr := manager.savePresentationLocked()
	manager.mu.Unlock()
	if persistErr != nil {
		return nil, persistErr
	}
	if err != nil {
		return nil, err
	}
	if err = validateRelease(release, manager.config.MaxDownload); err != nil {
		return nil, err
	}
	// 已校验的准备包是当前可执行的更新；检查不覆盖它，也不再次下载。
	manager.mu.Lock()
	if manager.prepared != nil {
		if comparison, _ := compareVersions(release.Version, manager.prepared.Version); comparison > 0 {
			manager.status.NewerVersion = release.Version
		}
		manager.readyLocked()
		manager.mu.Unlock()
		return nil, nil
	}
	manager.mu.Unlock()
	comparison, err := compareVersions(release.Version, manager.config.CurrentVersion)
	if err != nil {
		return nil, err
	}
	if comparison <= 0 {
		manager.mu.Lock()
		manager.release = nil
		manager.mu.Unlock()
		manager.set(func(status *Status) { status.Phase = Latest; status.AvailableVersion = ""; status.Notes = "" })
		return nil, nil
	}
	blocked, loadErr := manager.blockedVersion()
	if loadErr != nil {
		return nil, loadErr
	}
	copy := *release
	copy.Deltas = append([]Delta(nil), release.Deltas...)
	returned := copy
	returned.Deltas = append([]Delta(nil), copy.Deltas...)
	manager.mu.Lock()
	manager.release = &copy
	manager.manualRetry = manager.manualRetry && blocked == release.Version
	manager.presentation.Version, manager.presentation.Notes = release.Version, release.Notes
	err = manager.savePresentationLocked()
	manager.mu.Unlock()
	if err != nil {
		return nil, err
	}
	manager.set(func(status *Status) {
		status.Phase = Available
		status.AvailableVersion = release.Version
		status.Notes = release.Notes
		status.Downloaded = 0
		status.Total = release.Size
	})
	if blocked == release.Version {
		return nil, ErrBlocked
	}
	return &returned, nil
}

// Run performs only checks/downloads. It NEVER replaces files or restarts.
// Pass live preferences through policy; automatic retries do not override a block.
func (manager *Manager) Run(ctx context.Context, policy func() (check, download bool)) {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			check, download := false, false
			if policy != nil {
				check, download = policy()
			}
			if check {
				release, err := manager.Check(ctx, false)
				if err == nil && release != nil && download {
					_, _ = manager.Prepare(ctx)
				}
			}
			timer.Reset(24 * time.Hour)
		}
	}
}

func validFilename(name string) bool {
	if name == "" || len(name) > 180 || name == "." || name == ".." || strings.ContainsAny(name, "/\\:\x00<>\"|?*\r\n") || strings.TrimRight(name, ". ") != name {
		return false
	}
	base := strings.ToUpper(strings.Split(name, ".")[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || strings.HasPrefix(base, "COM") && len(base) == 4 && base[3] >= '1' && base[3] <= '9' || strings.HasPrefix(base, "LPT") && len(base) == 4 && base[3] >= '1' && base[3] <= '9' {
		return false
	}
	return true
}

func stableVersion(value string) ([3]uint64, error) {
	var version [3]uint64
	value = strings.TrimPrefix(value, "v")
	core, metadata, hasMetadata := strings.Cut(value, "+")
	if hasMetadata {
		if metadata == "" {
			return version, ErrVersion
		}
		for _, character := range metadata {
			if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '.' || character == '-') {
				return version, ErrVersion
			}
		}
		for _, part := range strings.Split(metadata, ".") {
			if part == "" {
				return version, ErrVersion
			}
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version, ErrVersion
	}
	for index, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return version, ErrVersion
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return version, ErrVersion
			}
		}
		if _, err := fmt.Sscan(part, &version[index]); err != nil {
			return version, ErrVersion
		}
	}
	return version, nil
}
func compareVersions(left, right string) (int, error) {
	a, err := stableVersion(left)
	if err != nil {
		return 0, err
	}
	b, err := stableVersion(right)
	if err != nil {
		return 0, err
	}
	for index := range a {
		if a[index] < b[index] {
			return -1, nil
		}
		if a[index] > b[index] {
			return 1, nil
		}
	}
	return 0, nil
}
