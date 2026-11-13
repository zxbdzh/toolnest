package ui

import (
	"fmt"
	"strings"

	native "github.com/egoist/mygo/ui"

	"toolnest/internal/app"
)

func (controller *Controller) currentPage(context *native.Context, width float32) {
	if width < 1000 {
		native.Segmented(context.Key("current-scope"), &controller.narrowScope, "用户 PATH", "系统 PATH")
		if controller.narrowScope == 0 {
			controller.pathPane(context, "current-user", "用户 PATH", controller.view.User, &controller.panes[0], &controller.pathLists[0])
		} else {
			controller.pathPane(context, "current-system", "系统 PATH", controller.view.System, &controller.panes[1], &controller.pathLists[1])
		}
		return
	}
	native.SplitPercent(context.Key("current-split"), &controller.split, func() {
		native.Column(context.Key("left-pane")).Fill().Padding(0, 12, 0, 0).Children(func() {
			controller.pathPane(context, "current-user", "用户 PATH", controller.view.User, &controller.panes[0], &controller.pathLists[0])
		})
	}, func() {
		native.Column(context.Key("right-pane")).Fill().Padding(0, 0, 0, 12).Children(func() {
			controller.pathPane(context, "current-system", "系统 PATH", controller.view.System, &controller.panes[1], &controller.pathLists[1])
		})
	}).Grow(1).MinHeight(0).SplitReset(50)
}

func (controller *Controller) pathPane(context *native.Context, key, title string, scope app.Scope, pane *pathPane, list *native.ListState) {
	theme := context.Theme()
	native.Column(context.Key(key)).Grow(1).MinWidth(0).MinHeight(0).Gap(14).Padding(18).Radius(14).Background(panelColor(theme)).Border(1, theme.Border.Alpha(0.7)).Children(func() {
		native.Row(context).Gap(10).Children(func() {
			icon := userScopeIcon
			if strings.Contains(key, "system") {
				icon = systemScopeIcon
			}
			native.Row(context).Width(36).Height(36).Justify(native.Center).Background(theme.Accent.Alpha(0.09)).Radius(9).Children(func() {
				native.Icon(context, icon).Width(18).Height(18).TextColor(theme.Accent)
			})
			native.Column(context).Grow(1).MinWidth(0).Gap(4).Children(func() {
				native.Text(context, title).FontSize(16).FontWeight(600)
				native.Text(context, scopeSummary(scope)).FontSize(12).TextColor(theme.TextMuted).SingleLine()
			})
			if native.Button(context, "复制完整值").Label(title+"复制完整值").Height(34).FontSize(13).Border(0, native.Transparent).Disabled(!scope.Exists).Clicked() {
				controller.actions.Copy(scope.Text)
			}
		})
		if native.SearchField(context.Key("search"), &pane.query).Placeholder("搜索路径，保留原始顺序").Label(title+"搜索").Height(36).Border(0, native.Transparent).Background(inputColor(theme)).FillWidth().Changed() {
			pane.selected = -1
			*list = native.ListState{}
		}
		entries := filterEntries(scope.Entries, pane.query)
		list.Selected = &pane.selected
		list.Key = func(row int) any { return entries[row].Index }
		list.Label = func(row int) string { return entryText(entries[row]) }
		columns := []native.TableColumn{{Title: "序号", Width: 58, Fixed: true}, {Title: "路径 · 只读", Width: 680}}
		if len(entries) == 0 {
			heading, reason := "没有匹配的路径", "清空搜索，查看保留原序号的全部路径。"
			if !scope.Exists {
				heading, reason = "尚未设置 "+title, "注册表中没有此值；只读查看不会创建它。"
			} else if len(scope.Entries) == 0 {
				heading, reason = "PATH 值为空", "完整复制仍保留原始内容，不合并空项。"
			}
			emptyState(context, heading, reason)
		} else {
			// 沿用原生 Table 的键盘、选中和横向滚动，不以手绘行替代可访问控件。
			if native.Table(context.Key("entries"), list, columns, len(entries), func(row, column int) {
				if column == 0 {
					native.Textf(context, "%02d", entries[row].Index).Font("Cascadia Mono").FontSize(12).TextColor(theme.TextMuted).Height(28)
					return
				}
				native.Text(context, entryText(entries[row])).SingleLine().Font("Cascadia Mono").FontSize(13).Height(28).Tooltip(entries[row].Text)
			}).Grow(1).MinHeight(0).Border(0, native.Transparent).Radius(8).Submitted() && pane.selected >= 0 && pane.selected < len(entries) {
				controller.showText(title+" · 完整路径", entries[pane.selected].Text)
			}
		}
		native.Row(context).Gap(8).Children(func() {
			native.Textf(context, "%d / %d 项", len(entries), len(scope.Entries)).FontSize(12).TextColor(context.Theme().TextMuted).Grow(1)
			valid := pane.selected >= 0 && pane.selected < len(entries)
			if native.Button(context, "查看原值").Label(title+"查看选中原值").Height(30).FontSize(12).Border(0, native.Transparent).Disabled(!valid).Clicked() {
				controller.showText(title+" · 完整路径", entries[pane.selected].Text)
			}
			if native.Button(context, "复制选中").Label(title+"复制选中").Height(30).FontSize(12).Border(0, native.Transparent).Disabled(!valid).Clicked() {
				controller.actions.Copy(entries[pane.selected].Text)
			}
		})
	})
}

func (controller *Controller) snapshotsPage(context *native.Context, width float32) {
	native.Row(context).Gap(8).Children(func() {
		native.SearchField(context.Key("snapshot-search"), &controller.snapshotQuery).Placeholder("搜索快照时间、来源或摘要").Label("搜索快照").Height(36).Border(0, native.Transparent).Background(inputColor(context.Theme())).Grow(1)
		count := len(controller.selectedSnapshots())
		if native.Button(context, fmt.Sprintf("删除选中 (%d)", count)).Disabled(count == 0 || controller.busy()).Clicked() {
			controller.deleteIDs = controller.selectedSnapshots()
			controller.openModal("delete")
		}
	})
	filtered := controller.filteredSnapshots()
	list := func() { controller.snapshotRows(context, filtered) }
	detail := func() { controller.snapshotDetail(context) }
	if width < 1000 {
		if controller.snapshotDetailOpen {
			if native.Button(context, "返回快照列表").Clicked() {
				controller.snapshotDetailOpen = false
			}
			native.Column(context.Key("snapshot-detail-narrow")).Grow(1).MinHeight(0).Children(detail)
		} else {
			native.Column(context.Key("snapshot-list-narrow")).Grow(1).MinHeight(0).Children(list)
			if native.Button(context, "打开所选快照").Disabled(controller.snapshotID == "").Clicked() {
				controller.snapshotDetailOpen = true
			}
		}
	} else {
		native.SplitPercent(context.Key("snapshot-split"), &controller.snapshotSplit, func() {
			native.Column(context).Fill().Padding(0, 12, 0, 0).Children(list)
		}, func() {
			native.Column(context).Fill().Padding(0, 0, 0, 12).Children(detail)
		}).Grow(1).MinHeight(0).SplitReset(34)
	}
}

func (controller *Controller) filteredSnapshots() []app.Snapshot {
	query := strings.ToLower(controller.snapshotQuery)
	var filtered []app.Snapshot
	for _, snapshot := range controller.view.Snapshots {
		if strings.Contains(strings.ToLower(snapshot.CreatedAt+" "+snapshot.Source+" "+snapshot.Summary+" "+snapshot.ID), query) {
			filtered = append(filtered, snapshot)
		}
	}
	return filtered
}

func (controller *Controller) selectedSnapshots() []string {
	var ids []string
	for _, snapshot := range controller.view.Snapshots {
		if controller.checked[snapshot.ID] {
			ids = append(ids, snapshot.ID)
		}
	}
	return ids
}

func (controller *Controller) snapshotRows(context *native.Context, snapshots []app.Snapshot) {
	native.Column(context.Key("snapshot-list")).Fill().Gap(12).Padding(16).Background(panelColor(context.Theme())).Border(1, context.Theme().Border.Alpha(0.7)).Radius(14).Children(func() {
		native.Textf(context, "%d 份快照 · 手动清理前始终保留", len(snapshots)).FontSize(12).TextColor(context.Theme().TextMuted)
		if len(snapshots) == 0 {
			emptyState(context, "暂无匹配快照", "使用顶栏「立即备份」创建还原点。")
			return
		}
		controller.snapshotList.Selected = &controller.snapshotSelected
		controller.snapshotList.Key = func(row int) any { return snapshots[row].ID }
		controller.snapshotList.Label = func(row int) string { return snapshots[row].CreatedAt + " " + snapshots[row].Summary }
		// 详情紧邻选择列固定在左侧，最小窗口也不需要先横向滚动才能进入快照。
		columns := []native.TableColumn{{Title: "选", Width: 36, Fixed: true}, {Title: "详情", Width: 52, Fixed: true}, {Title: "快照", Width: 236}}
		table := native.Table(context.Key("snapshots"), &controller.snapshotList, columns, len(snapshots), func(row, column int) {
			snapshot := snapshots[row]
			switch column {
			case 0:
				checked := controller.checked[snapshot.ID]
				if native.Checkbox(context, &checked, "").Label("选择快照 " + snapshot.ID).Changed() {
					controller.checked[snapshot.ID] = checked
				}
			case 2:
				native.Column(context).Height(56).Justify(native.Center).Gap(6).Children(func() {
					native.Text(context, snapshot.CreatedAt).FontSize(13).FontWeight(600).SingleLine()
					text := operationLabel(snapshot.Source) + " · " + snapshot.Summary
					color := context.Theme().TextMuted
					if snapshot.Error != "" {
						text, color = "不可使用："+snapshot.Error, context.Theme().Danger
					}
					native.Text(context, text).FontSize(12).TextColor(color).SingleLine().Tooltip(text)
				})
			case 1:
				// 显式详情按钮让键盘与辅助技术不必通过不可调用的时间文本猜测行操作。
				if native.Button(context, "查看").Label("查看快照 "+snapshot.ID).Height(30).FontSize(12).Padding(0, 6).Border(0, native.Transparent).Clicked() {
					controller.snapshotSelected, controller.snapshotID = row, snapshot.ID
					controller.snapshotDetailOpen = true
					controller.snapshotPanes[0].selected, controller.snapshotPanes[1].selected = -1, -1
					controller.snapshotLists = [2]native.ListState{}
				}
			}
		}).Grow(1).MinHeight(0).Border(0, native.Transparent).Radius(8)
		if (table.Changed() || table.Submitted()) && controller.snapshotSelected >= 0 && controller.snapshotSelected < len(snapshots) {
			controller.snapshotID = snapshots[controller.snapshotSelected].ID
			if table.Submitted() {
				controller.snapshotDetailOpen = true
			}
			controller.snapshotPanes[0].selected, controller.snapshotPanes[1].selected = -1, -1
			controller.snapshotLists = [2]native.ListState{}
		}
	})
}

func (controller *Controller) currentSnapshot() (app.Snapshot, bool) {
	for _, snapshot := range controller.view.Snapshots {
		if snapshot.ID == controller.snapshotID {
			return snapshot, true
		}
	}
	return app.Snapshot{}, false
}

func (controller *Controller) snapshotDetail(context *native.Context) {
	native.Column(context.Key("snapshot-detail")).Fill().Padding(18).Gap(14).Background(panelColor(context.Theme())).Border(1, context.Theme().Border.Alpha(0.7)).Radius(14).Children(func() {
		snapshot, exists := controller.currentSnapshot()
		if !exists {
			emptyState(context, "查看历史还原点", "选择快照，核对原值、比较差异或恢复。")
			return
		}
		native.Row(context).Gap(8).Children(func() {
			native.Text(context, snapshot.CreatedAt).FontSize(15).Bold().Grow(1).SingleLine()
			if native.PrimaryButton(context, "恢复快照…").Disabled(snapshot.Error != "" || controller.busy()).Clicked() {
				controller.beginRestore(snapshot.ID)
			}
		})
		native.Text(context, operationLabel(snapshot.Source)+" · "+snapshot.ID).FontSize(12).SingleLine().TextColor(context.Theme().TextMuted)
		if snapshot.Error != "" {
			native.Text(context, snapshot.Error).TextColor(context.Theme().Danger).Selectable()
			return
		}
		native.Segmented(context.Key("snapshot-detail-tabs"), &controller.snapshotTab, "用户 PATH", "系统 PATH", "与当前比较").Height(36)
		switch controller.snapshotTab {
		case 0:
			controller.pathPane(context, "snapshot-user", "快照用户 PATH", snapshot.User, &controller.snapshotPanes[0], &controller.snapshotLists[0])
		case 1:
			controller.pathPane(context, "snapshot-system", "快照系统 PATH", snapshot.System, &controller.snapshotPanes[1], &controller.snapshotLists[1])
		case 2:
			native.Text(context, "方向：所选快照 → 当前 PATH（只读比较，不执行恢复）").FontSize(12).TextColor(context.Theme().TextMuted)
			changes := append(displayChanges(snapshot.User, controller.view.User, "用户"), displayChanges(snapshot.System, controller.view.System, "系统")...)
			controller.changesView(context, changes)
		}
	})
}

func (controller *Controller) changesView(context *native.Context, changes []app.Change) {
	if len(changes) == 0 {
		native.Text(context, "所选范围的可见内容没有差异；恢复安全校验以业务层原始数据为准。").TextColor(context.Theme().TextMuted)
	}
	columns := []native.TableColumn{{Title: "范围 / 变化", Width: 150}, {Title: "之前", Width: 420}, {Title: "之后", Width: 420}}
	native.Table(context.Key("changes"), nil, columns, len(changes), func(row, column int) {
		change := changes[row]
		text := change.Scope + " · " + change.Kind
		if column == 1 {
			text = change.Before
		}
		if column == 2 {
			text = change.After
		}
		native.Text(context, text).SingleLine().Height(28).Tooltip(text).Selectable()
	}).Grow(1).MinHeight(0).Border(0, native.Transparent).Radius(8)
}

func (controller *Controller) records(context *native.Context) {
	native.Row(context).Gap(12).Children(func() {
		native.Text(context, "执行记录").FontSize(16).FontWeight(600)
		native.SearchField(context.Key("record-search"), &controller.recordQuery).Placeholder("筛选记录").Label("搜索执行记录").Height(36).Border(0, native.Transparent).Background(inputColor(context.Theme())).Grow(1)
	})
	query := strings.ToLower(controller.recordQuery)
	var records []app.Record
	for _, record := range controller.view.Records {
		if strings.Contains(strings.ToLower(record.At+record.Operation+record.Trigger+record.Outcome+record.Message+record.SnapshotID), query) {
			records = append(records, record)
		}
	}
	if len(records) == 0 {
		emptyState(context, "暂无匹配记录", "执行完成后将在这里显示真实结果。")
		return
	}
	columns := []native.TableColumn{{Title: "时间", Width: 168}, {Title: "操作", Width: 100}, {Title: "触发", Width: 85}, {Title: "结果", Width: 100}, {Title: "说明", Width: 450}, {Title: "详情", Width: 64, Fixed: true}}
	native.Table(context.Key("records"), &controller.recordList, columns, len(records), func(row, column int) {
		record := records[row]
		values := []string{record.At, record.Operation, operationLabel(record.Trigger), operationLabel(record.Outcome), record.Message}
		if column < len(values) {
			native.Text(context, values[column]).SingleLine().Height(28)
			return
		}
		if native.Button(context, "查看").Label("查看记录 " + record.At + " " + record.Operation).Clicked() {
			if result, ok := resultFromRecord(record); ok {
				controller.resultShown = result
				controller.openModal("operation-result")
			} else {
				controller.showText("执行记录", fmt.Sprintf("开始：%s\n结束：%s\n操作：%s\n触发：%s\n结果：%s\n快照：%s\n\n%s", record.At, record.FinishedAt, record.Operation, record.Trigger, record.Outcome, record.SnapshotID, record.Message))
			}
		}
	}).Grow(1).MinHeight(0).Border(0, native.Transparent).Radius(8).Background(panelColor(context.Theme()))
}
