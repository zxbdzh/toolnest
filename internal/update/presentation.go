package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"toolnest/internal/network"
)

// 展示元数据独立存放，旧版本回退仍能读取原来的 prepared.json。
type presentation struct {
	Version          string    `json:"version"`
	Notes            string    `json:"notes"`
	LastCheck        time.Time `json:"lastCheck"`
	PreparedAt       time.Time `json:"preparedAt"`
	DismissedVersion string    `json:"dismissedVersion"`
}

func (manager *Manager) loadPresentation() {
	if readJSON(filepath.Join(manager.config.StateDir, "presentation.json"), &manager.presentation) != nil {
		manager.presentation = presentation{}
	}
	manager.status.LastCheck = manager.presentation.LastCheck
	manager.status.DismissedVersion = manager.presentation.DismissedVersion
}

func (manager *Manager) savePresentationLocked() error {
	return writeJSON(filepath.Join(manager.config.StateDir, "presentation.json"), manager.presentation)
}

func (manager *Manager) readyLocked() {
	manager.status.Phase = Ready
	manager.status.AvailableVersion = manager.prepared.Version
	manager.status.Downloaded, manager.status.Total = manager.prepared.Size, manager.prepared.Size
	manager.status.BytesPerSecond = 0
	if manager.presentation.Version == manager.prepared.Version {
		manager.status.Notes = manager.presentation.Notes
		manager.status.PreparedAt = manager.presentation.PreparedAt
	}
}

func (manager *Manager) DismissNotice(version string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if version == "" && manager.status.Phase != Failed || version != manager.status.AvailableVersion && version != manager.config.CurrentVersion {
		return ErrVersion
	}
	previous := manager.presentation
	manager.presentation.DismissedVersion = string(manager.status.Phase) + ":" + version
	if err := manager.savePresentationLocked(); err != nil {
		manager.presentation = previous
		return err
	}
	manager.status.DismissedVersion = manager.presentation.DismissedVersion
	return nil
}

// 失败版本仅由单独的用户确认授权重试，普通检查不解除阻止。
func (manager *Manager) RetryBlocked(ctx context.Context) (*Prepared, error) {
	manager.mu.Lock()
	if manager.working || manager.networkChanging || manager.status.Phase != Blocked {
		manager.mu.Unlock()
		return nil, ErrNotReady
	}
	version := manager.status.AvailableVersion
	manager.mu.Unlock()
	if _, err := manager.Check(ctx, true); err != nil && !errors.Is(err, ErrBlocked) {
		return nil, err
	}
	manager.mu.Lock()
	if manager.release == nil || manager.release.Version != version || manager.status.Phase != Blocked {
		manager.mu.Unlock()
		return nil, ErrVersion
	}
	manager.manualRetry = true
	manager.mu.Unlock()
	return manager.Prepare(ctx)
}

func failureCode(err error) string {
	var status *network.HTTPStatusError
	if errors.As(err, &status) {
		return fmt.Sprintf("http-%d", status.StatusCode)
	}
	for _, entry := range []struct {
		err  error
		code string
	}{
		{network.ErrProxy, "proxy"}, {network.ErrCredentials, "credentials"}, {network.ErrNetwork, "network"},
		{network.ErrRedirect, "redirect"}, {network.ErrInvalidConfig, "network-config"},
		{ErrSignature, "signature"}, {ErrArchive, "archive"}, {ErrSize, "size"}, {ErrVersion, "version"},
		{ErrBlocked, "blocked"}, {ErrStartup, "startup"}, {ErrRecovery, "recovery"},
		{ErrNotConfigured, "unconfigured"}, {ErrNotReady, "not-ready"}, {ErrBusy, "busy"},
		{os.ErrPermission, "permission"}, {os.ErrNotExist, "missing-file"},
	} {
		if errors.Is(err, entry.err) {
			return entry.code
		}
	}
	return "other"
}
