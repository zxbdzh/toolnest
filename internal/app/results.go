package app

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"toolnest/internal/core"
)

func cloneResult(result OperationResult) OperationResult {
	result.Scopes = slices.Clone(result.Scopes)
	return result
}

func (controller *Controller) setResult(result OperationResult) {
	result.ID = core.NewID()
	controller.mu.Lock()
	controller.view.Result = cloneResult(result)
	controller.mu.Unlock()
}

func backupResult(snapshot core.Snapshot, err error) OperationResult {
	result := OperationResult{Operation: "手动备份", Outcome: "success", Title: "快照已保存", SnapshotID: snapshot.ID}
	if err != nil {
		result.Outcome, result.Title = "failed", "快照未能保存"
		result.Message, result.Details = "当前 PATH 没有被修改。修复下方问题后，可重新备份。", err.Error()
		result.SnapshotID = ""
		return result
	}
	result.Message = fmt.Sprintf("已保存用户 PATH %d 项、系统 PATH %d 项，并完成读回校验。", len(snapshot.State.User.Entries()), len(snapshot.State.System.Entries()))
	return result
}

func restoreResult(plan core.RestorePlan, restored core.RestoreResult) OperationResult {
	result := OperationResult{Operation: "恢复 PATH", Outcome: restored.Outcome, ProtectionID: restored.ProtectionID, SnapshotID: plan.Target.ID}
	result.Details = errorText(restored.Error)
	switch restored.Outcome {
	case "success":
		result.Title = "所选 PATH 已恢复"
		result.Message = "已读回核对所选范围。需要撤回时，可使用恢复前自动保存的保护快照；已打开的终端需重新打开。"
	case "rolled-back":
		result.Title = "恢复未完成，已回滚"
		result.Message = "本次写入未完成，已核对并恢复操作前的状态。请查看各范围结果。"
	case "partial":
		result.Title = "部分范围尚未恢复"
		result.Message = "请先核对各范围的实际结果。保护快照已保留，不会自动再次写入。"
	case "cancelled":
		result.Title, result.Message = "恢复已取消", "没有开始写入 PATH。"
	default:
		result.Title, result.Message = "恢复尚未开始", "前置检查未通过，没有开始写入 PATH。请处理原因后重新预览。"
	}
	statusLabels := map[string]string{"not-written": "未写入", "restored": "已恢复并核对", "rolled-back": "已回滚并核对",
		"unknown": "无法读取当前状态", "changed-externally": "检测到其他程序修改，已停止覆盖", "rollback-incomplete": "回滚未完成，请核对"}
	for _, scope := range plan.Scopes {
		name := "用户 PATH"
		if scope == core.System {
			name = "系统 PATH"
		}
		status := "未写入"
		for _, actual := range restored.Scopes {
			if actual.Scope == scope {
				status = statusLabels[actual.Status]
				if status == "" {
					status = "需要核对"
				}
			}
		}
		result.Scopes = append(result.Scopes, ScopeResult{Name: name, Status: status})
	}
	if restored.NotificationError != nil {
		result.Message += " 环境变更通知未完成，请重新打开终端或相关应用。"
		result.Details = errors.Join(restored.Error, restored.NotificationError).Error()
	}
	return result
}

func directoryProblem(err error) Problem {
	problem := Problem{Code: "snapshot-directory", Title: "快照目录暂不可用", Message: "备份和恢复暂不可用。当前 PATH 没有被修改，请在数据设置中核对快照位置。",
		Details: err.Error(), Action: "data"}
	if errors.Is(err, os.ErrNotExist) {
		problem.Code, problem.Title = "snapshot-directory-missing", "找不到快照目录"
		problem.Message = "请确认保存快照的磁盘和目录是否可用。不会自动创建替代目录或丢弃已有快照。"
	} else if errors.Is(err, os.ErrPermission) {
		problem.Code, problem.Title = "snapshot-directory-permission", "没有快照目录访问权限"
		problem.Message = "请核对目录权限；也可在数据设置中选择可访问的位置。当前 PATH 没有被修改。"
	}
	return problem
}
