package core

import (
	"errors"
	"fmt"
	"time"
)

type Schedule struct {
	Enabled    bool      `json:"enabled"`
	Mode       string    `json:"mode"`
	Hours      int       `json:"hours"`
	Hour       int       `json:"hour"`
	Minute     int       `json:"minute"`
	Revision   uint64    `json:"revision"`
	Next       time.Time `json:"next"`
	RetryAt    time.Time `json:"retry_at"`
	RetryUsed  bool      `json:"retry_used"`
	LastDate   string    `json:"last_date"`
	LastResult string    `json:"last_result"`
	Zone       string    `json:"zone"`
	ZoneOffset int       `json:"zone_offset"`
}

type Due struct {
	Revision uint64
	Trigger  string
	At       time.Time
}

func (schedule Schedule) Validate() error {
	if schedule.Mode != "interval" && schedule.Mode != "daily" {
		return errors.New("计划模式不支持")
	}
	if schedule.Hours < 1 || schedule.Hours > 168 {
		return errors.New("间隔必须为 1–168 的整数小时")
	}
	if schedule.Hour < 0 || schedule.Hour > 23 || schedule.Minute < 0 || schedule.Minute > 59 {
		return errors.New("每天执行时间无效")
	}
	return nil
}

func (schedule *Schedule) Configure(enabled bool, mode string, hours, hour, minute int, now time.Time) error {
	proposed := *schedule
	proposed.Enabled, proposed.Mode, proposed.Hours, proposed.Hour, proposed.Minute = enabled, mode, hours, hour, minute
	if err := proposed.Validate(); err != nil {
		return err
	}
	if schedule.Enabled == enabled && schedule.Mode == mode && schedule.Hours == hours && schedule.Hour == hour && schedule.Minute == minute {
		return nil
	}
	proposed.Revision++
	proposed.RetryAt, proposed.Next = time.Time{}, time.Time{}
	proposed.RetryUsed = false
	proposed.LastDate = ""
	proposed.Zone, proposed.ZoneOffset = now.Zone()
	if enabled {
		proposed.Next = now
	}
	*schedule = proposed
	return nil
}

func nextDaily(now time.Time, hour, minute int, lastDate string) time.Time {
	for offset := 0; offset < 4; offset++ {
		date := now.AddDate(0, 0, offset)
		candidate := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, now.Location())
		if candidate.After(now) && candidate.Format("2006-01-02") != lastDate {
			return candidate
		}
	}
	return now.Add(24 * time.Hour)
}

func (schedule *Schedule) Due(now time.Time) (Due, bool) {
	if !schedule.Enabled {
		return Due{}, false
	}
	zone, offset := now.Zone()
	if schedule.Mode == "daily" && (schedule.Zone != zone || schedule.ZoneOffset != offset) {
		schedule.Zone, schedule.ZoneOffset = zone, offset
		candidate := time.Date(now.Year(), now.Month(), now.Day(), schedule.Hour, schedule.Minute, 0, 0, now.Location())
		if schedule.LastDate < now.Format("2006-01-02") && !candidate.After(now) {
			schedule.Next = now
		} else {
			schedule.Next = nextDaily(now, schedule.Hour, schedule.Minute, schedule.LastDate)
		}
	}
	retry := !schedule.RetryAt.IsZero() && !now.Before(schedule.RetryAt)
	normal := !schedule.Next.IsZero() && !now.Before(schedule.Next)
	if !normal && !retry {
		return Due{}, false
	}
	trigger := "automatic"
	at := schedule.Next
	if retry {
		trigger = "retry"
		at = schedule.RetryAt
	}
	if normal && now.Sub(schedule.Next) > time.Minute {
		trigger = "catch-up"
	}
	if normal && schedule.Mode == "daily" && schedule.LastDate >= now.Format("2006-01-02") {
		schedule.Next = nextDaily(now, schedule.Hour, schedule.Minute, schedule.LastDate)
		if !retry {
			return Due{}, false
		}
	}
	return Due{schedule.Revision, trigger, at}, true
}

func (schedule *Schedule) Complete(due Due, now time.Time, outcome string) {
	if due.Revision != schedule.Revision || !schedule.Enabled {
		return
	}
	schedule.LastResult = outcome
	isRetry := due.Trigger == "retry" || (!schedule.RetryAt.IsZero() && !now.Before(schedule.RetryAt))
	if !isRetry {
		// 重试预算属于本次正常到期；下一周期的独立故障仍应有一次重试。
		schedule.RetryUsed = false
	}
	if outcome == "failed" {
		if !isRetry && !schedule.RetryUsed {
			schedule.RetryAt = now.Add(5 * time.Minute)
			schedule.RetryUsed = true
		} else {
			schedule.RetryAt = time.Time{}
		}
	} else {
		schedule.RetryAt = time.Time{}
		schedule.RetryUsed = false
	}
	if schedule.Mode == "daily" {
		// 跨午夜的失败重试不占用新一天正常计划的执行名额。
		if due.Trigger != "retry" || !schedule.Next.After(now) {
			schedule.LastDate = now.Format("2006-01-02")
		}
		schedule.Next = nextDaily(now, schedule.Hour, schedule.Minute, schedule.LastDate)
	} else {
		interval := time.Duration(schedule.Hours) * time.Hour
		if schedule.Next.IsZero() {
			schedule.Next = now.Add(interval)
		}
		if !schedule.Next.After(now) {
			schedule.Next = schedule.Next.Add((now.Sub(schedule.Next)/interval + 1) * interval)
		}
	}
}

func (schedule *Schedule) Stop() { schedule.RetryAt = time.Time{} }

func (schedule Schedule) Description() string {
	if !schedule.Enabled {
		return "自动备份已关闭"
	}
	if !schedule.RetryAt.IsZero() && schedule.RetryAt.Before(schedule.Next) {
		return "失败重试 " + schedule.RetryAt.Local().Format("01-02 15:04")
	}
	return fmt.Sprintf("下次检查 %s", schedule.Next.Local().Format("01-02 15:04"))
}
