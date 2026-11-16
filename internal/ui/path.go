package ui

import (
	"fmt"
	"strings"

	native "github.com/egoist/mygo/ui"

	"toolnest/internal/app"
)

func (controller *Controller) currentPage(context *native.Context, width float32) {
	if width < 800 {
		segmentedControl(context, "current-scope", &controller.narrowScope, "用户 PATH", "系统 PATH")
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
	native.Column(context.Key(key)).Grow(1).MinWidth(0).MinHeight(0).Gap(5).Padding(0, 0).Children(func() {
		native.Row(context).Gap(10).Children(func() {
			icon := userScopeIcon
			if strings.Contains(key, "system") {
				icon = systemScopeIcon
			}
			native.Icon(context, icon).Width(18).Height(18).TextColor(theme.TextMuted)
			native.Column(context).Grow(1).MinWidth(0).Gap(2).Children(func() {
				native.Text(context, title).FontSize(controlTextSize(context)).FontWeight(600)
				native.Text(context, scopeSummary(scope)).FontSize(detailSize(context)).TextColor(theme.TextMuted).SingleLine()
			})
			if quietButton(context, "复制完整值", controlOptions{label: title + "复制完整值", icon: copyIcon, disabled: !scope.Exists}).Clicked() {
				controller.actions.Copy(scope.Text)
			}
		})
		if searchField(context, "search", &pane.query, "搜索路径，保留原始顺序", title+"搜索").FillWidth().Changed() {
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
			if dataTable(context, "entries", list, columns, len(entries), func(row, column int) {
				if column == 0 {
					tableCellText(context, fmt.Sprintf("%02d", entries[row].Index), true, true)
					return
				}
				tableCellText(context, entryText(entries[row]), true, false)
			}).Submitted() && pane.selected >= 0 && pane.selected < len(entries) {
				controller.showText(title+" · 完整路径", entries[pane.selected].Text)
			}
		}
		native.Row(context).Gap(8).Children(func() {
			native.Textf(context, "%d / %d 项", len(entries), len(scope.Entries)).FontSize(detailSize(context)).TextColor(context.Theme().TextMuted).Grow(1)
			valid := pane.selected >= 0 && pane.selected < len(entries)
			if secondaryButton(context, "查看原值", controlOptions{key: "view-" + key, label: title + "查看选中原值", icon: eyeIcon, disabled: !valid, height: 34}).Clicked() {
				controller.showText(title+" · 完整路径", entries[pane.selected].Text)
			}
			if secondaryButton(context, "复制选中", controlOptions{key: "copy-" + key, label: title + "复制选中", icon: copyIcon, disabled: !valid, height: 34}).Clicked() {
				controller.actions.Copy(entries[pane.selected].Text)
			}
		})
	})
}

func (controller *Controller) snapshotsPage(context *native.Context, width float32) {
	// 未明确选择时默认展示最新可读快照；详情区不再用一整块空白等用户点击。
	if controller.snapshotID == "" {
		_, latest := validSnapshotSummary(controller.view.Snapshots)
		if latest.ID != "" {
			controller.snapshotID = latest.ID
			for index, snapshot := range controller.view.Snapshots {
				if snapshot.ID == latest.ID {
					controller.snapshotSelected = index
				}
			}
		}
	}
	native.Column(context).FillWidth().Gap(6).Children(func() {
		native.Row(context).FillWidth().Gap(10).AlignItems(native.Center).Children(func() {
			native.Text(context, "快照库").FontSize(controlTextSize(context)).TextColor(context.Theme().TextMuted)
			searchField(context, "snapshot-search", &controller.snapshotQuery, "搜索时间、来源或摘要", "搜索快照").Grow(1)
			count := len(controller.selectedSnapshots())
			if dangerButton(context, fmt.Sprintf("删除 (%d)", count), controlOptions{key: "delete-selected", label: fmt.Sprintf("删除选中 (%d)", count), disabled: count == 0 || controller.busy()}).Clicked() {
				controller.deleteIDs = controller.selectedSnapshots()
				controller.openModal("delete")
			}
		})
		controller.snapshotToolbar(context, width)
	})
	filtered := controller.filteredSnapshots()
	list := func() { controller.snapshotRows(context, width, filtered) }
	detail := func() { controller.snapshotDetail(context) }
	if width < 900 {
		if controller.snapshotDetailOpen {
			if secondaryButton(context, "返回快照列表", controlOptions{key: "snapshot-back"}).Clicked() {
				controller.snapshotDetailOpen = false
			}
			native.Column(context.Key("snapshot-detail-narrow")).Grow(1).MinHeight(0).Children(detail)
		} else {
			native.Column(context.Key("snapshot-list-narrow")).Grow(1).MinHeight(0).Children(list)
			if secondaryButton(context, "打开所选快照", controlOptions{key: "snapshot-open", disabled: controller.snapshotID == ""}).Clicked() {
				controller.snapshotDetailOpen = true
			}
		}
	} else {
		native.SplitPercent(context.Key("snapshot-split"), &controller.snapshotSplit, func() {
			native.Column(context).Fill().Padding(0, 10, 0, 0).Children(list)
		}, func() {
			native.Column(context).Fill().Padding(0, 0, 0, 8).Children(detail)
		}).Grow(1).MinHeight(0).SplitReset(36)
	}
}

// 快照列表和详情共享一条分组头；数字与入口都来自当前可见数据，不另放装饰卡片。
func (controller *Controller) snapshotToolbar(context *native.Context, width float32) {
	theme := context.Theme()
	filtered := controller.filteredSnapshots()
	count, latest := validSnapshotSummary(filtered)
	native.Row(context.Key("snapshot-toolbar")).FillWidth().MinHeight(28).Gap(8).AlignItems(native.Center).Children(func() {
		validText := fmt.Sprintf("%d 份可用", count)
		if count != len(filtered) {
			validText += fmt.Sprintf(" · %d 份需检查", len(filtered)-count)
		}
		native.Text(context, validText).FontSize(detailSize(context)).TextColor(theme.TextMuted)
		if count > 0 && width >= 900 {
			native.Text(context, "最近 "+displayTime(latest.CreatedAt)+" · "+operationLabel(latest.Source)+" · "+scopeSummary(latest.User)+" / "+scopeSummary(latest.System)).
				FontSize(detailSize(context)).TextColor(theme.TextMuted).Grow(1).MinWidth(0).SingleLine().Tooltip("最近 " + displayTime(latest.CreatedAt) + " · " + operationLabel(latest.Source))
		} else {
			native.Spacer(context)
		}
	})
}

func (controller *Controller) filteredSnapshots() []app.Snapshot {
	query := strings.ToLower(controller.snapshotQuery)
	var filtered []app.Snapshot
	for _, snapshot := range controller.view.Snapshots {
		if strings.Contains(strings.ToLower(snapshot.CreatedAt+" "+displayTime(snapshot.CreatedAt)+" "+snapshot.Source+" "+operationLabel(snapshot.Source)+" "+snapshot.Summary+" "+snapshot.ID), query) {
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

func (controller *Controller) snapshotRows(context *native.Context, width float32, snapshots []app.Snapshot) {
	theme := context.Theme()
	// 外层按实际行数收口，剩余空间由真实统计字段行承接；行数多时表格继续自行滚动。
	native.Column(context.Key("snapshot-list")).FillWidth().Grow(1).MinHeight(0).Gap(6).Padding(0, 0).Children(func() {
		if len(snapshots) == 0 {
			emptyState(context, "暂无匹配快照", "使用顶栏「立即备份」创建还原点。")
			return
		}
		controller.snapshotList.Selected = &controller.snapshotSelected
		controller.snapshotList.Key = func(row int) any { return snapshots[row].ID }
		controller.snapshotList.Label = func(row int) string { return snapshots[row].CreatedAt + " " + snapshots[row].Summary }
		// 快照是主从浏览而非数据表。原生 List 保留虚拟化、方向键和 Enter；
		// 列表行的实底由本页按钮统一绘制，不再被 Table 强制换成整块 Accent。
		native.Row(context).Height(30).Padding(0, 12).Gap(8).AlignItems(native.Center).Children(func() {
			native.Text(context, "选择").Width(24).FontSize(detailSize(context)).TextColor(theme.TextMuted)
			native.Text(context, "创建时间 / 内容").FontSize(detailSize(context)).TextColor(theme.TextMuted)
		})
		// 原生 List 仍管理选择和键盘；仅在此列表移除默认 Accent 整行底，
		// 防止它从内嵌圆角的外缘漏出。勾选和操作按钮保留完整原主题。
		listTheme := *theme
		listTheme.Accent, listTheme.InactiveAccent, listTheme.SurfaceHover = native.Transparent, native.Transparent, native.Transparent
		listTheme.Background = native.Transparent
		var table native.Element
		native.Themed(context.Key("snapshot-list-surface"), &listTheme).Grow(1).MinHeight(0).FillWidth().Children(func() {
			table = native.List(context.Key("snapshots"), &controller.snapshotList, len(snapshots), func(row int) {
				snapshot := snapshots[row]
				selected := controller.snapshotSelected == row
				background := native.Transparent
				if selected {
					background = selectionColor(theme)
				}
				line := native.Themed(context.Key("snapshot-row-"+snapshot.ID), theme).Row().FillWidth().Height(58).
					Margin(2, 0).Padding(0, 10).Gap(8).Radius(11).AlignItems(native.Center).
					Background(background).Transition(colorTransition(context))
				line.Children(func() {
					checked := controller.checked[snapshot.ID]
					// 按钮在辅助功能树中是叶节点，勾选框必须与查看按钮并列，
					// 才能独立被键盘、读屏和 UIA 操作，且勾选不会触发查看。
					if native.Checkbox(context.Key("select-"+snapshot.ID), &checked, "").Width(24).Label("选择快照 " + snapshot.ID).Changed() {
						controller.checked[snapshot.ID] = checked
					}
					button := native.ButtonBase(context.Key("view-"+snapshot.ID)).Label("查看快照 "+snapshot.ID).
						Grow(1).MinWidth(0).Height(58).Padding(0, 0).Gap(10).AlignItems(native.Center).
						Radius(7).Background(native.Transparent).TextColor(theme.Text).FocusRing(false).Transition(colorTransition(context))
					if button.Hovered() && !selected {
						button.Background(theme.SurfaceHover)
					}
					button.Children(func() {
						native.Column(context).Grow(1).MinWidth(0).Gap(3).Justify(native.Center).Children(func() {
							native.Text(context, displayTime(snapshot.CreatedAt)).FontSize(controlTextSize(context)).FontWeight(500).SingleLine().Tooltip(snapshot.CreatedAt)
							text := operationLabel(snapshot.Source) + " · " + snapshot.Summary
							if snapshot.Error != "" {
								text = "不可使用：" + snapshot.Error
							}
							native.Text(context, text).FontSize(detailSize(context)).TextColor(theme.TextMuted).SingleLine().Tooltip(text)
						})
						native.Icon(context, chevronRightIcon).Width(14).Height(14).TextColor(theme.TextMuted)
					})
					if button.Clicked() {
						controller.snapshotSelected, controller.snapshotID = row, snapshot.ID
						if width >= 900 {
							controller.snapshotDetailOpen = true
						}
						controller.snapshotPanes[0].selected, controller.snapshotPanes[1].selected = -1, -1
						controller.snapshotLists = [2]native.ListState{}
					}
				})
			}).Grow(1).MinHeight(0).Padding(0, 5).Radius(panelRadius).Background(native.Transparent)
		})
		// 行内按钮负责鼠标查看，表格仍负责方向键、Enter 和可访问行选择。
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
	theme := context.Theme()
	native.Column(context.Key("snapshot-detail")).Fill().Padding(0, 0).Gap(8).Children(func() {
		detailPanel := sectionPanel(context, "snapshot-detail-panel").Grow(1).MinHeight(0).FillWidth().Padding(12, 16).Gap(6).
			Border(1, theme.Border.Alpha(0.35)).Background(panelColor(theme).Alpha(0.22)).Radius(panelRadius)
		detailPanel.Children(func() {
			snapshot, exists := controller.currentSnapshot()
			if !exists {
				// 未选中时不再占满整块居中空白，把有用的入口和信息放在内容起点。
				native.Column(context).FillWidth().Gap(8).Padding(12, 0).Children(func() {
					native.Text(context, "选择一份快照").FontSize(sectionSize(context)).FontWeight(600)
					native.Text(context, "查看原值、比较差异，确认后再恢复 PATH。").FontSize(controlTextSize(context)).TextColor(theme.TextMuted).LineHeight(1.4)
					divider(context)
					native.Row(context).Gap(8).Wrap().Children(func() {
						if secondaryButton(context, "选择最近快照", controlOptions{key: "select-latest", disabled: len(controller.view.Snapshots) == 0, height: 34}).Clicked() {
							_, latest := validSnapshotSummary(controller.view.Snapshots)
							controller.openSnapshot(latest.ID)
						}
						if secondaryButton(context, "查看 PATH", controlOptions{key: "view-path", height: 34}).Clicked() {
							controller.page, controller.tab = "path", 0
						}
					})
					native.Text(context, "快照会一直保留，直到你手动删除。").FontSize(detailSize(context)).TextColor(theme.TextMuted)
				})
				return
			}
			native.Row(context).Gap(8).AlignItems(native.Center).Children(func() {
				native.Text(context, displayTime(snapshot.CreatedAt)).FontSize(sectionSize(context)).FontWeight(500).Grow(1).SingleLine().Tooltip(snapshot.CreatedAt)
				if primaryButton(context, "恢复快照…", controlOptions{key: "restore-snapshot", disabled: snapshot.Error != "" || controller.busy()}).Clicked() {
					controller.beginRestore(snapshot.ID)
				}
			})
			native.Text(context, operationLabel(snapshot.Source)+" · "+snapshot.ID).FontSize(detailSize(context)).SingleLine().TextColor(theme.TextMuted)
			if snapshot.Error != "" {
				native.Text(context, snapshot.Error).FontSize(controlTextSize(context)).TextColor(theme.Danger).Selectable()
				return
			}
			divider(context)
			// 切换明细页签前先给出两个范围的真实状态，不让右侧空白承担信息层级。
			// 两个范围用一行字段而非嵌套小卡，层级交给分隔线与表格。
			native.Row(context).FillWidth().MinHeight(34).Gap(16).AlignItems(native.Center).Children(func() {
				for _, scope := range []struct {
					label string
					value app.Scope
				}{{"用户 PATH", snapshot.User}, {"系统 PATH", snapshot.System}} {
					native.Row(context).Grow(1).MinWidth(0).Gap(7).AlignItems(native.Center).Children(func() {
						native.Text(context, scope.label).FontSize(detailSize(context)).TextColor(theme.TextMuted)
						native.Text(context, scopeSummary(scope.value)).FontSize(detailSize(context)).Grow(1).MinWidth(0).SingleLine().Tooltip(scopeSummary(scope.value))
					})
				}
			})
			divider(context)
			segmentedControl(context, "snapshot-detail-tabs", &controller.snapshotTab, "用户 PATH", "系统 PATH", "与当前比较")
			switch controller.snapshotTab {
			case 0:
				controller.pathPane(context, "snapshot-user", "快照用户 PATH", snapshot.User, &controller.snapshotPanes[0], &controller.snapshotLists[0])
			case 1:
				controller.pathPane(context, "snapshot-system", "快照系统 PATH", snapshot.System, &controller.snapshotPanes[1], &controller.snapshotLists[1])
			case 2:
				native.Text(context, "方向：所选快照 → 当前 PATH（只读比较，不执行恢复）").FontSize(detailSize(context)).TextColor(theme.TextMuted)
				changes := append(displayChanges(snapshot.User, controller.view.User, "用户"), displayChanges(snapshot.System, controller.view.System, "系统")...)
				controller.changesView(context, changes)
			}
		})
		controller.snapshotRecord(context)
	})
}

// 详情页底部用所选快照对应的真实记录承接空间；没有记录时也不编造内容。
func (controller *Controller) snapshotRecord(context *native.Context) {
	snapshot, exists := controller.currentSnapshot()
	if !exists {
		return
	}
	var record *app.Record
	for index := range controller.view.Records {
		if controller.view.Records[index].SnapshotID == snapshot.ID {
			record = &controller.view.Records[index]
			break
		}
	}
	if record == nil {
		return
	}
	sectionHeader(context, "执行记录", nil, "全部记录", func() { controller.page, controller.tab = "path", 3 })
	sectionPanel(context, "snapshot-record").Padding(4, 0).Children(func() {
		native.Row(context).MinHeight(38).Gap(10).AlignItems(native.Center).Children(func() {
			native.Box(context).Width(5).Height(5).Radius(3).Background(context.Theme().TextMuted)
			native.Column(context).Grow(1).MinWidth(0).Children(func() {
				native.Text(context, record.Message).FontSize(controlTextSize(context)).FontWeight(500).SingleLine().Tooltip(record.Message)
				native.Text(context, displayTime(record.At)+" · "+operationLabel(record.Outcome)).FontSize(detailSize(context)).TextColor(context.Theme().TextMuted).SingleLine()
			})
		})
	})
}

func (controller *Controller) changesView(context *native.Context, changes []app.Change) {
	if len(changes) == 0 {
		native.Text(context, "所选范围的可见内容没有差异；恢复安全校验以业务层原始数据为准。").FontSize(detailSize(context)).TextColor(context.Theme().TextMuted)
	}
	columns := []native.TableColumn{{Title: "范围 / 变化", Width: 150}, {Title: "之前", Width: 420}, {Title: "之后", Width: 420}}
	dataTable(context, "changes", nil, columns, len(changes), func(row, column int) {
		change := changes[row]
		text := change.Scope + " · " + change.Kind
		if column == 1 {
			text = change.Before
		}
		if column == 2 {
			text = change.After
		}
		native.Text(context, text).FontSize(controlTextSize(context)).SingleLine().Height(tableRowHeight - 12).Tooltip(text).Selectable()
	})
}

func (controller *Controller) records(context *native.Context) {
	native.Row(context).FillWidth().Gap(12).Children(func() {
		searchField(context, "record-search", &controller.recordQuery, "筛选记录", "搜索执行记录").Grow(1)
	})
	query := strings.ToLower(controller.recordQuery)
	var records []app.Record
	for _, record := range controller.view.Records {
		if strings.Contains(strings.ToLower(strings.Join([]string{record.At, displayTime(record.At), record.Operation, record.Trigger, operationLabel(record.Trigger), record.Outcome, operationLabel(record.Outcome), record.Message, record.SnapshotID}, " ")), query) {
			records = append(records, record)
		}
	}
	if len(records) == 0 {
		emptyState(context, "暂无匹配记录", "执行完成后将在这里显示真实结果。")
		return
	}
	columns := []native.TableColumn{{Title: "时间", Width: 168}, {Title: "操作", Width: 100}, {Title: "触发", Width: 85}, {Title: "结果", Width: 100}, {Title: "说明", Width: 450}, {Title: "详情", Width: 64, Fixed: true}}
	dataTable(context, "records", &controller.recordList, columns, len(records), func(row, column int) {
		record := records[row]
		values := []string{displayTime(record.At), record.Operation, operationLabel(record.Trigger), operationLabel(record.Outcome), record.Message}
		if column < len(values) {
			tableCellText(context, values[column], false, false)
			return
		}
		if iconButton(context, eyeIcon, controlOptions{key: "record-" + record.At, label: "查看记录 " + record.At + " " + record.Operation, height: 30}).Clicked() {
			if result, ok := resultFromRecord(record); ok {
				controller.resultShown = result
				controller.openModal("operation-result")
			} else {
				controller.showText("执行记录", fmt.Sprintf("开始：%s\n结束：%s\n操作：%s\n触发：%s\n结果：%s\n快照：%s\n\n%s", record.At, record.FinishedAt, record.Operation, record.Trigger, record.Outcome, record.SnapshotID, record.Message))
			}
		}
	})
}
