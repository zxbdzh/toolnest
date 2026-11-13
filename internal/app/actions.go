package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"toolnest/internal/core"
	"toolnest/internal/network"
	"toolnest/internal/update"
)

func (controller *Controller) SaveSettings(settings Settings) {
	controller.run("保存设置", func(ctx context.Context) (string, string, error) {
		return "", "", controller.saveSettings(ctx, settings)
	})
}

func (controller *Controller) saveSettings(ctx context.Context, settings Settings) error {
	if err := validateSettings(settings); err != nil {
		return err
	}
	controller.mu.RLock()
	previous := controller.config
	controller.mu.RUnlock()
	proxyChanged := settings.ProxyMode != previous.Settings.ProxyMode || settings.ProxyURL != previous.Settings.ProxyURL || settings.ProxyUsername != previous.Settings.ProxyUsername || settings.ProxyPassword != ""
	if proxyChanged {
		release, err := controller.Updater().HoldNetworkPolicy()
		if err != nil {
			return errors.New("更新请求正在进行，请先取消检查／下载，再保存代理设置")
		}
		defer release()
	}
	if settings.SnapshotDir != previous.Settings.SnapshotDir {
		return errors.New("更换快照目录请使用复制校验流程")
	}
	next := previous
	next.Settings = settings
	next.Settings.ProxyPassword = ""
	if err := next.Schedule.Configure(settings.ScheduleEnabled, settings.ScheduleMode, settings.IntervalHours, settings.DailyHour, settings.DailyMinute, time.Now()); err != nil {
		return err
	}
	netConfig := network.Config{Mode: network.Mode(settings.ProxyMode)}
	newCredential := ""
	if !proxyChanged {
		netConfig = controller.networkConfig()
	} else if settings.ProxyMode == "custom" {
		netConfig.Address = settings.ProxyURL
		if settings.ProxyUsername != "" || settings.ProxyPassword != "" {
			if controller.vault == nil {
				return network.ErrCredentials
			}
			secret := network.Credentials{Username: settings.ProxyUsername, Password: settings.ProxyPassword}
			if secret.Password == "" && previous.CredentialID != "" {
				old, err := controller.vault.Read(ctx, previous.CredentialID)
				if err != nil {
					return network.ErrCredentials
				}
				secret.Password = old.Password
			}
			newCredential = "ToolNest/proxy/" + core.NewID()
			if err := controller.vault.Write(ctx, newCredential, secret); err != nil {
				return network.ErrCredentials
			}
			netConfig.CredentialID = newCredential
			next.CredentialID = newCredential
		} else {
			next.CredentialID = ""
		}
	} else {
		next.CredentialID = ""
	}
	cleanupCredential := func() {
		if newCredential != "" {
			_ = controller.vault.Delete(context.WithoutCancel(ctx), newCredential)
		}
	}
	if proxyChanged {
		probe, err := network.New(ctx, netConfig, controller.vault)
		if err != nil {
			cleanupCredential()
			return err
		}
		probe.Snapshot().CloseIdleConnections()
	}
	startupChanged := settings.LaunchAtLogin != previous.Settings.LaunchAtLogin
	if startupChanged && controller.hooks.SetStartup != nil {
		if err := controller.hooks.SetStartup(settings.LaunchAtLogin); err != nil {
			cleanupCredential()
			return err
		}
	}
	if err := saveConfig(controller.root, next); err != nil {
		var rollback error
		if startupChanged && controller.hooks.SetStartup != nil {
			rollback = controller.hooks.SetStartup(previous.Settings.LaunchAtLogin)
		}
		cleanupCredential()
		return errors.Join(err, rollback)
	}
	var networkErr error
	if proxyChanged {
		if controller.network == nil {
			controller.network, networkErr = network.New(ctx, netConfig, controller.vault)
		} else {
			networkErr = controller.network.Configure(ctx, netConfig, controller.vault)
		}
	}
	if err := networkErr; err != nil {
		rollback := saveConfig(controller.root, previous)
		if startupChanged && controller.hooks.SetStartup != nil {
			rollback = errors.Join(rollback, controller.hooks.SetStartup(previous.Settings.LaunchAtLogin))
		}
		cleanupCredential()
		return errors.Join(err, rollback)
	}
	controller.mu.Lock()
	controller.config = next
	controller.view.SettingsRevision++
	if controller.updateConfig.Network == nil && controller.network != nil {
		controller.updateConfig.Network = controller.network
		if restored, updateErr := update.New(controller.updateConfig); updateErr == nil {
			controller.updater = restored
		} else {
			controller.view.Error = updateErr.Error()
		}
	}
	controller.mu.Unlock()
	if previous.CredentialID != "" && previous.CredentialID != next.CredentialID && controller.vault != nil {
		_ = controller.vault.Delete(context.WithoutCancel(ctx), previous.CredentialID)
	}
	if controller.hooks.Theme != nil {
		controller.hooks.Theme(settings.Theme)
	}
	return nil
}

func (controller *Controller) CompleteOnboarding(settings Settings, skip bool) {
	controller.completeOnboarding(settings, skip, false)
}

func (controller *Controller) CompleteOnboardingWithBackup(settings Settings, backup bool) {
	controller.completeOnboarding(settings, false, backup)
}

func (controller *Controller) completeOnboarding(settings Settings, skip, backup bool) {
	controller.run("首次设置", func(ctx context.Context) (string, string, error) {
		if skip {
			controller.mu.RLock()
			settings = controller.config.Settings
			controller.mu.RUnlock()
		}
		settings.OnboardingDone = true
		if err := controller.saveSettings(ctx, settings); err != nil {
			return "", "", err
		}
		if backup && !skip {
			snapshot, _, err := controller.service.Backup(ctx, "manual")
			result := backupResult(snapshot, err)
			result.Operation = "首次设置"
			if err == nil {
				result.Title = "第一份快照已保存"
			}
			controller.setResult(result)
			return "", snapshot.ID, err
		}
		return "", "", nil
	})
}

func (controller *Controller) ImportLegacy() {
	controller.run("导入旧快照", func(context.Context) (string, string, error) {
		local := os.Getenv("LOCALAPPDATA")
		if !filepath.IsAbs(local) {
			return "failed", "", errors.New("无法确定旧数据目录")
		}
		result, err := controller.service.Store.Import(filepath.Join(local, "PathGuard", "snapshots"))
		if err != nil {
			return "failed", "", err
		}
		if len(result.Failed) > 0 {
			return "partial", "", fmt.Errorf("导入 %d，跳过 %d，失败 %d；原文件保留", result.Copied, result.Skipped, len(result.Failed))
		}
		controller.mu.Lock()
		controller.view.Status = fmt.Sprintf("导入 %d，跳过 %d；原文件保留", result.Copied, result.Skipped)
		controller.mu.Unlock()
		return "success", "", nil
	})
}

func (controller *Controller) MoveSnapshots(directory string) {
	controller.run("更换快照目录", func(context.Context) (string, string, error) {
		if err := core.CheckDirectory(directory); err != nil {
			return "failed", "", err
		}
		result, err := controller.service.Store.CopyTo(directory)
		if err != nil {
			return "failed", "", fmt.Errorf("已复制 %d 份，原目录继续生效: %w", result.Copied, err)
		}
		controller.mu.RLock()
		next := controller.config
		controller.mu.RUnlock()
		next.Settings.SnapshotDir = directory
		if err := saveConfig(controller.root, next); err != nil {
			return "failed", "", fmt.Errorf("副本已保留，设置未切换: %w", err)
		}
		controller.mu.Lock()
		controller.config = next
		controller.service.Store.Directory = directory
		controller.mu.Unlock()
		return "success", "", nil
	})
}

func (controller *Controller) DisableLegacy(id string) {
	controller.run("停用旧入口", func(context.Context) (string, string, error) {
		if controller.hooks.DisableLegacy == nil {
			return "failed", "", errors.New("旧入口适配不可用")
		}
		return "", "", controller.hooks.DisableLegacy(id)
	})
}
func (controller *Controller) UndoLegacy(id string) {
	controller.run("撤销旧入口停用", func(context.Context) (string, string, error) {
		if controller.hooks.UndoLegacy == nil {
			return "failed", "", errors.New("旧入口适配不可用")
		}
		return "", "", controller.hooks.UndoLegacy(id)
	})
}

func (controller *Controller) CheckUpdate() {
	go controller.checkUpdate(false)
}

func (controller *Controller) checkUpdate(automatic bool) {
	controller.mu.RLock()
	enabled := controller.config.Settings.AutoCheck && controller.view.PendingExit == ""
	controller.mu.RUnlock()
	if automatic && !enabled {
		return
	}
	manager := controller.Updater()
	release, err := manager.Check(controller.ctx, !automatic)
	if err != nil || release == nil {
		return
	}
	// 网络检查可能耗时，下载前重读最新偏好，不能继续使用检查开始时的旧开关。
	controller.mu.RLock()
	download := controller.config.Settings.AutoDownload && controller.view.PendingExit == "" && (!automatic || controller.config.Settings.AutoCheck)
	controller.mu.RUnlock()
	if download {
		_, _ = manager.Prepare(controller.ctx)
	}
}
func (controller *Controller) DownloadUpdate() {
	go func() {
		manager := controller.Updater()
		if _, err := manager.Prepare(controller.ctx); errors.Is(err, update.ErrNotReady) {
			if release, err := manager.Check(controller.ctx, true); err == nil && release != nil {
				_, _ = manager.Prepare(controller.ctx)
			}
		}
	}()
}

func (controller *Controller) OpenUpdateSource() {
	if controller.releasePage != "" && controller.hooks.OpenURL != nil {
		if err := controller.hooks.OpenURL(controller.releasePage); err != nil {
			controller.setError(err)
		}
	}
}

func (controller *Controller) CancelDownload() { controller.Updater().CancelNetwork() }
func (controller *Controller) RestartUpdate() {
	if controller.hooks.RestartUpdate == nil || controller.Updater().Prepared() == nil {
		controller.setError(errors.New("更新重启入口不可用"))
		return
	}
	controller.requestExit("restart")
}

func (controller *Controller) DismissUpdate(version string) {
	if err := controller.Updater().DismissNotice(version); err != nil {
		controller.setError(err)
	}
}

func (controller *Controller) RetryUpdate() {
	if err := controller.refresh(controller.ctx); err != nil {
		controller.setError(errors.New("请先处理数据读取问题，再重新尝试更新"))
		return
	}
	go func() {
		manager := controller.Updater()
		if manager.Status().Phase == update.Blocked {
			_, _ = manager.RetryBlocked(controller.ctx)
			return
		}
		if manager.Status().AvailableVersion != "" && manager.Prepared() == nil {
			_, _ = manager.Prepare(controller.ctx)
		} else {
			controller.CheckUpdate()
		}
	}()
}

func (controller *Controller) TestProxy(settings Settings) {
	controller.mu.Lock()
	if controller.view.ProxyTesting {
		controller.mu.Unlock()
		return
	}
	controller.view.ProxyTesting = true
	controller.mu.Unlock()
	go func() {
		defer func() { controller.mu.Lock(); controller.view.ProxyTesting = false; controller.mu.Unlock() }()
		controller.mu.RLock()
		configuration := controller.networkConfig()
		credentialID := controller.config.CredentialID
		controller.mu.RUnlock()
		configuration.Mode = network.Mode(settings.ProxyMode)
		configuration.Address = ""
		configuration.CredentialID = ""
		vault := controller.vault
		if settings.ProxyMode == "custom" {
			configuration.Address = settings.ProxyURL
			if settings.ProxyUsername != "" || settings.ProxyPassword != "" {
				secret := network.Credentials{Username: settings.ProxyUsername, Password: settings.ProxyPassword}
				if secret.Password == "" && credentialID != "" && vault != nil {
					if old, err := vault.Read(controller.ctx, credentialID); err == nil {
						secret.Password = old.Password
					}
				}
				configuration.CredentialID = "draft"
				vault = draftVault{secret}
			}
		}
		manager, err := network.New(controller.ctx, configuration, vault)
		if err != nil {
			controller.setError(err)
			return
		}
		defer manager.Snapshot().CloseIdleConnections()
		ctx, cancel := context.WithTimeout(controller.ctx, 30*time.Second)
		defer cancel()
		endpoint := controller.UpdateEndpoint()
		if endpoint == "" {
			controller.setError(update.ErrNotConfigured)
			return
		}
		result, err := manager.TestConnection(ctx, endpoint)
		if err != nil {
			var statusError *network.HTTPStatusError
			if errors.As(err, &statusError) {
				// 收到 HTTP 响应说明已经到达服务，不能把占位仓库 404 或限流归咎于代理失效。
				controller.mu.Lock()
				controller.view.Status = fmt.Sprintf("已收到更新服务器响应 · HTTP %d · 更新源不可用", statusError.StatusCode)
				detail := updateEndpointStatusText(statusError.StatusCode)
				if result.Endpoint == "https://api.github.com/repos/toolnest-test/toolnest/releases/latest" {
					detail += " 当前测试包使用占位更新仓库，尚未配置可用的正式更新源。"
				}
				controller.view.Error = fmt.Sprintf("已收到更新入口响应（%s）：%s", result.Endpoint, detail)
				controller.mu.Unlock()
			} else {
				controller.setError(fmt.Errorf("更新入口连接失败（%s）: %s", result.Endpoint, updateErrorText(err.Error())))
			}
			return
		}
		controller.mu.Lock()
		controller.view.Error = ""
		controller.view.Status = fmt.Sprintf("更新入口连接正常 · %d ms", result.Duration.Milliseconds())
		controller.mu.Unlock()
	}()
}

type draftVault struct{ credentials network.Credentials }

func (vault draftVault) Read(context.Context, string) (network.Credentials, error) {
	return vault.credentials, nil
}
func (draftVault) Write(context.Context, string, network.Credentials) error {
	return errors.New("草稿凭据不可持久保存")
}
func (draftVault) Delete(context.Context, string) error { return nil }

func (controller *Controller) Copy(text string) {
	if controller.hooks.Copy != nil {
		if err := controller.hooks.Copy(text); err != nil {
			controller.setError(err)
		}
	}
}
func (controller *Controller) ChooseDirectory() (string, error) {
	if controller.hooks.ChooseDirectory == nil {
		return "", errors.New("目录选择器不可用")
	}
	return controller.hooks.ChooseDirectory()
}
func (controller *Controller) OpenDataDirectory() {
	if controller.hooks.OpenDirectory != nil {
		if err := controller.hooks.OpenDirectory(controller.root); err != nil {
			controller.setError(err)
		}
	}
}

func (controller *Controller) RequestClose(behavior string, remember bool) {
	if behavior != "tray" && behavior != "exit" && behavior != "quit" {
		return
	}
	if behavior == "tray" {
		controller.mu.RLock()
		available := controller.view.TrayAvailable
		controller.mu.RUnlock()
		if !available || controller.hooks.Hide == nil {
			controller.setError(errors.New("托盘不可用，窗口保持显示"))
			return
		}
	}
	if remember {
		release, err := controller.TryAcquire("保存关闭偏好")
		if err != nil {
			controller.setError(err)
			return
		}
		controller.mu.RLock()
		next := controller.config
		controller.mu.RUnlock()
		next.Settings.CloseBehavior = behavior
		err = saveConfig(controller.root, next)
		if err == nil {
			controller.mu.Lock()
			controller.config = next
			controller.mu.Unlock()
		}
		release()
		if err != nil {
			controller.setError(err)
			return
		}
	}
	if behavior == "tray" {
		if err := controller.hooks.Hide(); err != nil {
			controller.setError(err)
		}
		return
	}
	controller.Quit()
}

func (controller *Controller) Quit() {
	controller.requestExit("quit")
}

func (controller *Controller) requestExit(intent string) {
	controller.mu.Lock()
	if controller.closed || controller.view.PendingExit != "" {
		controller.mu.Unlock()
		return
	}
	controller.exitGeneration++
	generation := controller.exitGeneration
	controller.view.PendingExit = intent
	controller.mu.Unlock()
	if controller.completeExit(generation) {
		return
	}
	go func() {
		timer := time.NewTicker(25 * time.Millisecond)
		defer timer.Stop()
		for {
			select {
			case <-controller.ctx.Done():
				return
			case <-timer.C:
				if controller.completeExit(generation) {
					return
				}
			}
		}
	}()
}

func (controller *Controller) CancelExit() {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if !controller.closed && !controller.view.ExitCommitting {
		controller.exitGeneration++
		controller.view.PendingExit = ""
	}
}

func (controller *Controller) completeExit(generation uint64) bool {
	controller.mu.RLock()
	active := generation == controller.exitGeneration && controller.view.PendingExit != "" && !controller.closed
	controller.mu.RUnlock()
	if !active {
		return true
	}
	release, err := controller.TryAcquire("安全退出")
	if err != nil {
		return false
	}
	defer release()
	controller.mu.Lock()
	if generation != controller.exitGeneration || controller.view.PendingExit == "" {
		controller.mu.Unlock()
		return true
	}
	intent := controller.view.PendingExit
	controller.view.ExitCommitting = true
	next := controller.config
	next.Schedule.Stop()
	controller.mu.Unlock()
	err = saveConfig(controller.root, next)
	if err == nil && intent == "restart" {
		err = controller.hooks.RestartUpdate()
	}
	if err != nil {
		controller.mu.Lock()
		controller.view.PendingExit, controller.view.Error = "", err.Error()
		controller.view.ExitCommitting = false
		controller.mu.Unlock()
		return true
	}
	controller.mu.Lock()
	controller.config = next
	controller.closed = true
	controller.mu.Unlock()
	controller.cancel()
	if controller.hooks.Quit != nil {
		controller.hooks.Quit()
	}
	return true
}

func (controller *Controller) tick(now time.Time) {
	controller.mu.Lock()
	_, pending := controller.config.Schedule.Due(now)
	controller.mu.Unlock()
	if !pending {
		return
	}
	release, err := controller.TryAcquire("自动检查")
	if err != nil {
		return
	}
	defer release()
	controller.mu.Lock()
	next := controller.config
	due, ok := next.Schedule.Due(now)
	if !ok {
		controller.config.Schedule = next.Schedule
		controller.mu.Unlock()
		return
	}
	controller.mu.Unlock()
	snapshot, saved, err := controller.service.Backup(controller.ctx, due.Trigger)
	outcome := "success"
	if !saved {
		outcome = "unchanged"
	}
	if err != nil {
		outcome = "failed"
	}
	next.Schedule.Complete(due, now, outcome)
	if persistErr := saveConfig(controller.root, next); persistErr != nil {
		err = errors.Join(err, persistErr)
		outcome = "failed"
	}
	controller.mu.Lock()
	controller.config = next
	if err != nil {
		controller.automaticError = err.Error()
		if controller.lastNotification != err.Error() {
			controller.lastNotification = err.Error()
			if controller.hooks.Notify != nil {
				go controller.hooks.Notify("自动备份失败", err.Error())
			}
		}
	} else {
		controller.automaticError = ""
		controller.lastNotification = ""
	}
	controller.mu.Unlock()
	controller.record(Record{At: now.Local().Format(time.RFC3339), FinishedAt: time.Now().Local().Format(time.RFC3339), Operation: "自动备份", Trigger: due.Trigger, Outcome: outcome, SnapshotID: snapshot.ID, Message: errorText(err)})
	controller.refresh(context.Background())
}
