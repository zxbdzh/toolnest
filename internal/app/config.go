package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"toolnest/internal/core"
)

type config struct {
	Version      int           `json:"version"`
	Settings     Settings      `json:"settings"`
	Schedule     core.Schedule `json:"schedule"`
	CredentialID string        `json:"credential_id,omitempty"`
}

func defaultConfig(root string) config {
	return config{Version: 1, Settings: Settings{
		Theme: "system", TextSize: "standard", CloseBehavior: "ask", StartInTray: true, SnapshotDir: filepath.Join(root, "snapshots"),
		ScheduleMode: "interval", IntervalHours: 6, DailyHour: 9, ProxyMode: "system", AutoCheck: true, AutoDownload: true,
	}, Schedule: core.Schedule{Mode: "interval", Hours: 6, Hour: 9}}
}

func loadConfig(root string) (config, error) {
	defaults := defaultConfig(root)
	filename := filepath.Join(root, "settings.json")
	data, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		data, err = os.ReadFile(filename + ".previous")
		if errors.Is(err, os.ErrNotExist) {
			return defaults, nil
		}
	}
	if err != nil {
		return defaults, err
	}
	if len(data) > 1024*1024 {
		return defaults, errors.New("设置文件超过大小上限")
	}
	loaded := defaults
	if err := json.Unmarshal(data, &loaded); err != nil {
		previous, previousErr := os.ReadFile(filename + ".previous")
		recovered := defaults
		if previousErr == nil && len(previous) <= 1024*1024 && json.Unmarshal(previous, &recovered) == nil && validateConfig(recovered) == nil {
			return recovered, fmt.Errorf("设置损坏，已读取上一份副本，请保存修复: %w", err)
		}
		return defaults, fmt.Errorf("设置读取失败，未启用计划: %w", err)
	}
	if err := validateConfig(loaded); err != nil {
		return defaults, err
	}
	return loaded, nil
}

func validateConfig(loaded config) error {
	if loaded.Version != 1 {
		return errors.New("设置版本不支持")
	}
	if err := validateSettings(loaded.Settings); err != nil {
		return err
	}
	if err := loaded.Schedule.Validate(); err != nil {
		return err
	}
	if loaded.Schedule.Enabled != loaded.Settings.ScheduleEnabled || loaded.Schedule.Mode != loaded.Settings.ScheduleMode || loaded.Schedule.Hours != loaded.Settings.IntervalHours || loaded.Schedule.Hour != loaded.Settings.DailyHour || loaded.Schedule.Minute != loaded.Settings.DailyMinute {
		return errors.New("计划配置与执行状态不一致，拒绝启用计划")
	}
	if loaded.Schedule.Enabled && loaded.Schedule.Next.IsZero() {
		return errors.New("已启用计划缺少下次运行时间")
	}
	return nil
}

func saveConfig(root string, value config) error {
	// 凭据由操作系统保存；公开配置永远不落明文密码。
	value.Settings.ProxyPassword = ""
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return core.AtomicWrite(filepath.Join(root, "settings.json"), data, true)
}

func validateSettings(value Settings) error {
	if value.Theme != "system" && value.Theme != "light" && value.Theme != "dark" {
		return errors.New("主题无效")
	}
	// 旧配置没有字号字段时按标准档兼容；未知值不猜测，拒绝写入以免界面尺寸失控。
	if value.TextSize != "" && value.TextSize != "compact" && value.TextSize != "standard" && value.TextSize != "comfortable" {
		return errors.New("字号无效")
	}
	if value.CloseBehavior != "ask" && value.CloseBehavior != "tray" && value.CloseBehavior != "quit" && value.CloseBehavior != "exit" {
		return errors.New("关闭行为无效")
	}
	if value.ProxyMode != "system" && value.ProxyMode != "direct" && value.ProxyMode != "custom" {
		return errors.New("代理模式无效")
	}
	if err := core.CheckDirectory(value.SnapshotDir); err != nil {
		return err
	}
	schedule := core.Schedule{Mode: value.ScheduleMode, Hours: value.IntervalHours, Hour: value.DailyHour, Minute: value.DailyMinute}
	return schedule.Validate()
}
