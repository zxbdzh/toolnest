package update

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type transaction struct {
	Version         string `json:"version"`
	PreviousVersion string `json:"previousVersion"`
	Target          string `json:"target"`
	Backup          string `json:"backup"`
	Candidate       string `json:"candidate"`
	Digest          string `json:"digest"`
	OldDigest       string `json:"oldDigest"`
	Token           string `json:"token"`
	Phase           string `json:"phase"`
	Failure         string `json:"failure,omitempty"`
}

type SwitchResult struct{ Version, Executable, Backup, Token string }

func (manager *Manager) journalPath() string {
	return filepath.Join(manager.config.StateDir, "switch.json")
}

// Apply is explicit and requires the app's exclusive write gate. Call before
// starting ordinary work, or from the helper AFTER its parent has exited.
func (manager *Manager) Apply(ctx context.Context) (result SwitchResult, err error) {
	if err = manager.begin(Applying); err != nil {
		return result, err
	}
	defer func() { manager.end(err) }()
	release, err := manager.config.Gate.TryAcquire("update-apply")
	if err != nil {
		return result, ErrBusy
	}
	defer release()
	unlock, err := manager.diskLock()
	if err != nil {
		return result, err
	}
	defer unlock()
	if err = ctx.Err(); err != nil {
		return result, err
	}
	var existing transaction
	if loadErr := readJSON(manager.journalPath(), &existing); loadErr == nil {
		if existing.Phase != "confirmed" && existing.Phase != "rolled-back" {
			return result, ErrBusy
		}
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return result, loadErr
	}
	manager.mu.Lock()
	prepared := manager.prepared
	manager.mu.Unlock()
	if err = manager.validatePrepared(prepared); err != nil {
		return result, err
	}
	if manager.config.CurrentVersion == prepared.Version {
		return result, ErrVersion
	}
	if comparison, versionErr := compareVersions(prepared.Version, manager.config.CurrentVersion); versionErr != nil || comparison <= 0 {
		return result, ErrVersion
	}
	if err = ensureDirectory(manager.config.InstallDir); err != nil {
		return result, err
	}
	identifier, err := randomToken()
	if err != nil {
		return result, err
	}
	target := filepath.Join(manager.config.InstallDir, manager.config.Executable)
	candidate := filepath.Join(manager.config.InstallDir, ".toolnest-new-"+identifier+".exe")
	backup := filepath.Join(manager.config.InstallDir, ".toolnest-old-"+identifier+".exe")
	oldDigest, err := fileDigest(target, manager.config.MaxExecutable)
	if err != nil {
		return result, err
	}
	// 不信任可编辑的暂存摘要：从已签名归档再次解包后才可换入。
	if err = extractExecutable(ctx, prepared.Archive, candidate, prepared.Version, manager.config.Executable, manager.config.MaxExecutable); err != nil {
		_ = os.Remove(candidate)
		return result, err
	}
	digest, err := fileDigest(candidate, manager.config.MaxExecutable)
	if err != nil {
		return result, err
	}
	journal := transaction{Version: prepared.Version, PreviousVersion: manager.config.CurrentVersion, Target: target, Backup: backup, Candidate: candidate, Digest: digest, OldDigest: oldDigest, Token: identifier, Phase: "staged"}
	if err = writeJSON(manager.journalPath(), journal); err != nil {
		_ = os.Remove(candidate)
		return result, err
	}
	// 父进程刚退出时，Windows 或安全软件可能短暂持有旧程序映射。
	// 只在真正改名前做有界等待；进入替换阶段后仍保持原事务和回退语义。
	if err = renameWhenReleased(ctx, manager.rename, target, backup); err != nil {
		journal.Phase = "rolled-back"
		journal.Failure = "installed executable is locked or not writable"
		_ = writeJSON(manager.journalPath(), journal)
		return result, err
	}
	journal.Phase = "old-moved"
	if err = writeJSON(manager.journalPath(), journal); err != nil {
		return result, manager.rollback(&journal, err)
	}
	if err = manager.rename(candidate, target); err != nil {
		return result, manager.rollback(&journal, err)
	}
	journal.Phase = "pending"
	if err = writeJSON(manager.journalPath(), journal); err != nil {
		return result, manager.rollback(&journal, err)
	}
	manager.set(func(status *Status) { status.Phase = PendingConfirmation })
	return SwitchResult{Version: journal.Version, Executable: target, Backup: backup, Token: identifier}, nil
}

func renameWhenReleased(ctx context.Context, rename func(string, string) error, from, to string) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := rename(from, to)
		if err == nil {
			return nil
		}
		// 权限拒绝、路径错误和人为长期占用不能靠无限重试掩盖；
		// 仅 Windows 的共享冲突交给短暂退出/扫描的释放窗口。
		if !errors.Is(err, syscall.Errno(32)) {
			return err
		}
		if time.Now().After(deadline) {
			return err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (manager *Manager) diskLock() (func(), error) {
	if err := os.MkdirAll(manager.config.StateDir, 0700); err != nil {
		return nil, err
	}
	return lockState(filepath.Join(manager.config.StateDir, "switch.lock"))
}

func (manager *Manager) validateTransaction(journal *transaction) error {
	if len(journal.Token) != 64 {
		return ErrArchive
	}
	if _, err := hex.DecodeString(journal.Token); err != nil {
		return ErrArchive
	}
	if _, err := stableVersion(journal.Version); err != nil {
		return err
	}
	if journal.Target != filepath.Join(manager.config.InstallDir, manager.config.Executable) || journal.Backup != filepath.Join(manager.config.InstallDir, ".toolnest-old-"+journal.Token+".exe") || journal.Candidate != filepath.Join(manager.config.InstallDir, ".toolnest-new-"+journal.Token+".exe") {
		return ErrArchive
	}
	return nil
}

func (manager *Manager) rollback(journal *transaction, cause error) error {
	if err := manager.validateTransaction(journal); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(manager.config.StateDir, "blocked.json"), struct {
		Version string `json:"version"`
	}{journal.Version}); err != nil {
		return fmt.Errorf("%w: cannot persist blocked release; backup=%s", ErrRecovery, journal.Backup)
	}
	manager.mu.Lock()
	manager.prepared = nil
	manager.mu.Unlock()
	if err := os.Remove(filepath.Join(manager.config.StateDir, "prepared.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: cannot disable failed prepared update; backup=%s", ErrRecovery, journal.Backup)
	}
	if digest, err := fileDigest(journal.Target, manager.config.MaxExecutable); err == nil && digest == journal.OldDigest {
		// 回退已换回旧程序但在写日志前断电时，按摘要识别完成，避免重复移动。
		journal.Phase = "rolled-back"
		journal.Failure = "previous executable restored"
		if err := writeJSON(manager.journalPath(), journal); err != nil {
			return err
		}
		return cause
	}
	if info, err := os.Lstat(journal.Target); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: replacement target is not a regular file; backup=%s", ErrRecovery, journal.Backup)
		}
		if digest, err := fileDigest(journal.Target, manager.config.MaxExecutable); err != nil || digest != journal.Digest {
			return fmt.Errorf("%w: replacement target changed; backup=%s", ErrRecovery, journal.Backup)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// 回退不删除失败新版，避免误删后来写入的文件，同时保留人工诊断入口。
	if _, err := os.Lstat(journal.Backup); err != nil {
		journal.Phase = "recovery-failed"
		journal.Failure = "retained previous executable is unavailable"
		_ = writeJSON(manager.journalPath(), journal)
		return fmt.Errorf("%w: backup=%s", ErrRecovery, journal.Backup)
	}
	if digest, err := fileDigest(journal.Backup, manager.config.MaxExecutable); err != nil || digest != journal.OldDigest {
		return fmt.Errorf("%w: previous executable changed; backup=%s", ErrRecovery, journal.Backup)
	}
	if _, err := os.Lstat(journal.Target); err == nil {
		failed := filepath.Join(manager.config.InstallDir, ".toolnest-failed-"+journal.Token+".exe")
		if err = manager.rename(journal.Target, failed); err != nil {
			journal.Phase = "recovery-failed"
			journal.Failure = "cannot preserve failed executable"
			_ = writeJSON(manager.journalPath(), journal)
			return fmt.Errorf("%w: backup=%s", ErrRecovery, journal.Backup)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := manager.rename(journal.Backup, journal.Target); err != nil {
		journal.Phase = "recovery-failed"
		journal.Failure = "cannot restore previous executable"
		_ = writeJSON(manager.journalPath(), journal)
		return fmt.Errorf("%w: backup=%s", ErrRecovery, journal.Backup)
	}
	journal.Phase = "rolled-back"
	journal.Failure = "updated application failed to start; previous executable restored"
	var startupFailure struct{ Token, Version, Message string }
	if readJSON(filepath.Join(manager.config.StateDir, "startup-failure.json"), &startupFailure) == nil &&
		startupFailure.Token == journal.Token && startupFailure.Version == journal.Version && startupFailure.Message != "" {
		journal.Failure += "\n启动检查：" + startupFailure.Message
	}
	if err := writeJSON(manager.journalPath(), journal); err != nil {
		return err
	}
	manager.set(func(status *Status) {
		status.Phase = Blocked
		status.Failure = journal.Failure
		status.RolledBack = true
	})
	return cause
}

func (manager *Manager) blockedVersion() (string, error) {
	var blocked struct {
		Version string `json:"version"`
	}
	if err := readJSON(filepath.Join(manager.config.StateDir, "blocked.json"), &blocked); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return blocked.Version, nil
}

// Recover must run before ordinary app writes. It resolves interrupted renames;
// an unconfirmed new process must let its helper handle rollback instead.
func (manager *Manager) Recover(ctx context.Context) error {
	if err := manager.begin(Applying); err != nil {
		return err
	}
	var outcome error
	defer func() { manager.end(outcome) }()
	release, err := manager.config.Gate.TryAcquire("update-recovery")
	if err != nil {
		outcome = ErrBusy
		return outcome
	}
	defer release()
	unlock, err := manager.diskLock()
	if err != nil {
		outcome = err
		return err
	}
	defer unlock()
	if err = ctx.Err(); err != nil {
		outcome = err
		return err
	}
	var journal transaction
	if err = readJSON(manager.journalPath(), &journal); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			manager.mu.Lock()
			manager.status.Phase = Unchecked
			if manager.prepared != nil {
				manager.readyLocked()
			}
			manager.mu.Unlock()
			return nil
		}
		outcome = err
		return err
	}
	if err = manager.validateTransaction(&journal); err != nil {
		outcome = err
		return err
	}
	switch journal.Phase {
	case "confirmed", "rolled-back":
		manager.mu.Lock()
		manager.status.Phase = Unchecked
		if journal.Phase == "confirmed" && journal.Version == manager.config.CurrentVersion {
			manager.status.Phase = Confirmed
			manager.status.Notes = manager.presentation.Notes
		} else if journal.Phase == "rolled-back" {
			manager.status.Phase = Blocked
			manager.status.AvailableVersion = journal.Version
			manager.status.Failure, manager.status.FailureCode = journal.Failure, "blocked"
			if manager.status.Failure == "" {
				manager.status.Failure = ErrBlocked.Error()
			}
			manager.status.RolledBack = journal.PreviousVersion == manager.config.CurrentVersion
			manager.status.RecoveryPath = journal.Target
		}
		if manager.prepared != nil {
			manager.readyLocked()
		}
		manager.mu.Unlock()
		return nil
	case "staged":
		if digest, err := fileDigest(journal.Target, manager.config.MaxExecutable); err == nil && digest == journal.OldDigest {
			journal.Phase = "rolled-back"
			outcome = writeJSON(manager.journalPath(), journal)
			manager.mu.Lock()
			manager.status.Phase = Unchecked
			if manager.prepared != nil {
				manager.readyLocked()
			}
			manager.mu.Unlock()
			return outcome
		}
	case "pending":
		if manager.hasConfirmation(&journal) {
			if digest, err := fileDigest(journal.Target, manager.config.MaxExecutable); err != nil || digest != journal.Digest {
				outcome = ErrSignature
				return outcome
			}
			journal.Phase = "confirmed"
			outcome = writeJSON(manager.journalPath(), journal)
			manager.set(func(status *Status) { status.Phase = Confirmed })
			return outcome
		}
		// 正在运行的新版不能给自己改名回退，由旧版助手退出/终止后执行。
		if manager.config.CurrentVersion == journal.Version {
			outcome = ErrStartup
			return outcome
		}
	case "old-moved", "recovery-failed":
	default:
		outcome = ErrArchive
		return outcome
	}
	outcome = manager.rollback(&journal, ErrStartup)
	return outcome
}

func (manager *Manager) hasConfirmation(journal *transaction) bool {
	var acknowledgement struct {
		Token   string `json:"token"`
		Version string `json:"version"`
	}
	if readJSON(filepath.Join(manager.config.StateDir, "startup.json"), &acknowledgement) != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(acknowledgement.Token), []byte(journal.Token)) == 1 && acknowledgement.Version == journal.Version
}

// ConfirmStartup is called AFTER UI, settings, snapshots and scheduling initialize.
// A successful handshake is not a claim that every user operation is healthy.
func (manager *Manager) ConfirmStartup(ctx context.Context, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var journal transaction
	if err := readJSON(manager.journalPath(), &journal); err != nil {
		return err
	}
	if err := manager.validateTransaction(&journal); err != nil {
		return err
	}
	if journal.Phase != "pending" || journal.Version != manager.config.CurrentVersion || subtle.ConstantTimeCompare([]byte(token), []byte(journal.Token)) != 1 {
		return ErrStartup
	}
	if digest, err := fileDigest(journal.Target, manager.config.MaxExecutable); err != nil || digest != journal.Digest {
		return ErrSignature
	}
	if err := writeJSON(filepath.Join(manager.config.StateDir, "startup.json"), struct {
		Token   string `json:"token"`
		Version string `json:"version"`
	}{token, journal.Version}); err != nil {
		return err
	}
	manager.set(func(status *Status) { status.Phase = Confirmed; status.Notes = manager.presentation.Notes })
	return nil
}

// 启动失败原因绑定本次事务，旧诊断不能被拼接到后来的更新尝试。
func (manager *Manager) RecordStartupFailure(token, message string) error {
	var journal transaction
	if err := readJSON(manager.journalPath(), &journal); err != nil {
		return err
	}
	if manager.validateTransaction(&journal) != nil || journal.Phase != "pending" || journal.Version != manager.config.CurrentVersion ||
		subtle.ConstantTimeCompare([]byte(token), []byte(journal.Token)) != 1 {
		return ErrStartup
	}
	if len(message) > 8192 {
		message = message[:8192]
	}
	return writeJSON(filepath.Join(manager.config.StateDir, "startup-failure.json"), struct {
		Token, Version, Message string
	}{token, journal.Version, message})
}

type Process interface {
	Wait() error
	Kill() error
}
type Launcher func(executable string, args []string) (Process, error)
type childProcess struct{ command *exec.Cmd }

func (child childProcess) Wait() error { return child.command.Wait() }
func (child childProcess) Kill() error { return child.command.Process.Kill() }

func StartProcess(executable string, args []string) (Process, error) {
	command := exec.Command(executable, args...)
	command.Dir = filepath.Dir(executable)
	configureProcess(command)
	if err := command.Start(); err != nil {
		return nil, err
	}
	return childProcess{command}, nil
}

// RestartInstalled is ONLY called following an explicit user restart or the
// safe next-startup path. It rolls back on spawn/exit/handshake failure.
func (manager *Manager) RestartInstalled(ctx context.Context, result SwitchResult, args []string, timeout time.Duration, launch Launcher) error {
	if err := manager.begin(PendingConfirmation); err != nil {
		return err
	}
	var outcome error
	defer func() { manager.end(outcome) }()
	release, err := manager.config.Gate.TryAcquire("update-restart")
	if err != nil {
		outcome = ErrBusy
		return outcome
	}
	defer release()
	unlock, err := manager.diskLock()
	if err != nil {
		outcome = err
		return err
	}
	defer unlock()
	outcome = manager.restartInstalled(ctx, result, args, timeout, launch)
	return outcome
}

func (manager *Manager) restartInstalled(ctx context.Context, result SwitchResult, args []string, timeout time.Duration, launch Launcher) error {
	if launch == nil {
		launch = StartProcess
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	var journal transaction
	if err := readJSON(manager.journalPath(), &journal); err != nil {
		return err
	}
	if err := manager.validateTransaction(&journal); err != nil {
		return err
	}
	if journal.Phase != "pending" || result.Executable != journal.Target || result.Token != journal.Token || result.Version != journal.Version {
		return ErrNotReady
	}
	for _, argument := range args {
		if strings.HasPrefix(argument, "--toolnest-update-") {
			return ErrArchive
		}
	}
	if digest, err := fileDigest(journal.Target, manager.config.MaxExecutable); err != nil || digest != journal.Digest {
		return ErrSignature
	}
	arguments := append(append([]string(nil), args...), "--toolnest-update-confirm", journal.Token)
	process, err := launch(journal.Target, arguments)
	if err != nil {
		return manager.rollback(&journal, ErrStartup)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if manager.hasConfirmation(&journal) {
				select {
				case <-done:
					return manager.rollback(&journal, ErrStartup)
				default:
				}
				journal.Phase = "confirmed"
				if err := writeJSON(manager.journalPath(), journal); err != nil {
					return err
				}
				manager.set(func(status *Status) { status.Phase = Confirmed })
				manager.mu.Lock()
				manager.prepared = nil
				manager.mu.Unlock()
				_ = os.Remove(filepath.Join(manager.config.StateDir, "prepared.json"))
				return nil
			}
		case <-done:
			return manager.rollback(&journal, ErrStartup)
		case <-ctx.Done():
			return manager.stopAndRollback(process, done, &journal, ctx.Err())
		case <-timer.C:
			if manager.hasConfirmation(&journal) {
				select {
				case <-done:
					return manager.rollback(&journal, ErrStartup)
				default:
				}
				journal.Phase = "confirmed"
				if err := writeJSON(manager.journalPath(), journal); err != nil {
					return err
				}
				manager.set(func(status *Status) { status.Phase = Confirmed })
				manager.mu.Lock()
				manager.prepared = nil
				manager.mu.Unlock()
				_ = os.Remove(filepath.Join(manager.config.StateDir, "prepared.json"))
				return nil
			}
			return manager.stopAndRollback(process, done, &journal, ErrStartup)
		}
	}
}
func (manager *Manager) stopAndRollback(process Process, done <-chan error, journal *transaction, cause error) error {
	if err := process.Kill(); err != nil {
		journal.Phase = "recovery-failed"
		journal.Failure = "new process could not be stopped"
		_ = writeJSON(manager.journalPath(), journal)
		_ = writeJSON(filepath.Join(manager.config.StateDir, "blocked.json"), struct {
			Version string `json:"version"`
		}{journal.Version})
		return fmt.Errorf("%w: new process could not be stopped; backup=%s", ErrRecovery, journal.Backup)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		return fmt.Errorf("%w: new process still running; backup=%s", ErrRecovery, journal.Backup)
	}
	return manager.rollback(journal, cause)
}

type HelperRequest struct {
	ParentPID int      `json:"parentPid"`
	Token     string   `json:"token"`
	Arguments []string `json:"arguments"`
	Version   string   `json:"version"`
}
type HelperCommand struct {
	Executable  string
	Arguments   []string
	RequestFile string
}

// HelperCommand builds arguments but does NOT start a process or request exit.
// main must recognize helper flags before single-instance/UI initialization.
func (manager *Manager) HelperCommand(ctx context.Context, args []string) (HelperCommand, error) {
	if err := ctx.Err(); err != nil {
		return HelperCommand{}, err
	}
	manager.mu.Lock()
	prepared := manager.prepared
	manager.mu.Unlock()
	if err := manager.validatePrepared(prepared); err != nil {
		return HelperCommand{}, err
	}
	for _, argument := range args {
		if strings.HasPrefix(argument, "--toolnest-update-") {
			return HelperCommand{}, ErrArchive
		}
	}
	token, err := randomToken()
	if err != nil {
		return HelperCommand{}, err
	}
	request := HelperRequest{ParentPID: os.Getpid(), Token: token, Arguments: append([]string(nil), args...), Version: prepared.Version}
	path := filepath.Join(manager.config.StateDir, "helper-"+token+".json")
	if err := writeJSON(path, request); err != nil {
		return HelperCommand{}, err
	}
	return HelperCommand{Executable: filepath.Join(manager.config.InstallDir, manager.config.Executable), Arguments: []string{"--toolnest-update-helper", path, token}, RequestFile: path}, nil
}

// RunHelper waits for its parent, applies the prepared update and supervises the
// new process. Never call from an ordinary UI or a hidden-to-tray transition.
func (manager *Manager) RunHelper(ctx context.Context, path, token string, launch Launcher) error {
	if filepath.Dir(path) != manager.config.StateDir || filepath.Base(path) != "helper-"+token+".json" || len(token) != 64 {
		return ErrArchive
	}
	var request HelperRequest
	if err := readJSON(path, &request); err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(request.Token)) != 1 || request.ParentPID == os.Getpid() {
		return ErrArchive
	}
	if err := waitParent(ctx, request.ParentPID); err != nil {
		return err
	}
	prepared := manager.Prepared()
	if prepared == nil || prepared.Version != request.Version {
		return ErrNotReady
	}
	result, err := manager.Apply(ctx)
	if err != nil {
		return err
	}
	err = manager.RestartInstalled(ctx, result, request.Arguments, 30*time.Second, launch)
	if errors.Is(err, ErrStartup) {
		if launch == nil {
			launch = StartProcess
		}
		_, restartErr := launch(filepath.Join(manager.config.InstallDir, manager.config.Executable), request.Arguments)
		if restartErr != nil {
			return errors.Join(err, errors.New("previous executable restored but could not be restarted"))
		}
	}
	return err
}

func ensureDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrArchive
	}
	return nil
}
func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
