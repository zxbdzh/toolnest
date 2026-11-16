// Package ui 实现 ToolNest 的 MyGo 原生界面，系统操作仅通过 app.Actions 提交。
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	native "github.com/egoist/mygo/ui"

	"toolnest/internal/app"
)

// Controller 的状态归 UI 线程所有；New 不启动窗口、托盘或后台业务服务。
type Controller struct {
	actions            app.Actions
	window             windowControls
	view               app.View
	events             chan func()
	pending            string
	localError         string
	page               string
	settingsSection    int
	tab                int
	narrowScope        int
	split              float32
	snapshotSplit      float32
	panes              [2]pathPane
	pathLists          [2]native.ListState
	snapshotPanes      [2]pathPane
	snapshotLists      [2]native.ListState
	snapshotQuery      string
	snapshotList       native.ListState
	snapshotSelected   int
	snapshotID         string
	snapshotTab        int
	snapshotDetailOpen bool
	checked            map[string]bool
	recordQuery        string
	recordList         native.ListState
	draft              app.Settings
	base               app.Settings
	initialized        bool
	submitted          *app.Settings
	submittedSchedule  bool
	submittedRevision  uint64
	modal              string
	modalOpen          bool
	modalError         string
	preview            app.Preview
	previewReady       bool
	previewGeneration  uint64
	restoreUser        bool
	restoreSystem      bool
	deleteIDs          []string
	moveTarget         string
	legacyID           string
	rememberClose      bool
	closeChoice        string
	onboarding         app.Settings
	onboardingShown    bool
	textTitle          string
	textDetail         string
	dismissedUpdate    string
	initialBackup      bool
	dismissedResult    string
	resultShown        app.OperationResult
	systemDark         bool
}

// New 创建可嵌入主代理窗口的界面。actions.View 必须提供线程安全的只读副本。
func New(actions app.Actions) *Controller {
	return &Controller{
		actions: actions, events: make(chan func(), 16), page: "home",
		split: 50, snapshotSplit: 34, snapshotSelected: -1,
		panes:         [2]pathPane{{selected: -1}, {selected: -1}},
		snapshotPanes: [2]pathPane{{selected: -1}, {selected: -1}},
		checked:       make(map[string]bool), closeChoice: "tray", initialBackup: true,
	}
}

// Content 返回纯 Go 绘制内容，不创建 WebView，不接管主程序的服务生命周期。
func (controller *Controller) Content() *native.Content { return native.View(controller.render) }

// WindowOptions 让主入口和独立 UI 共用窗口外观，避免出现两层标题栏。
func (controller *Controller) WindowOptions(title string) mygo.WindowOptions {
	return mygo.WindowOptions{
		Title: title, Width: 1180, Height: 780, MinWidth: 620, MinHeight: 520,
		TitleBarStyle: mygo.TitleBarHidden, HideWindowButtons: true,
		Vibrancy: mygo.VibrancyAcrylic, Content: controller.Content(),
	}
}

// AttachWindow 接入关闭确认。主代理仍负责实现 RequestClose、托盘与安全退出。
func (controller *Controller) AttachWindow(window *mygo.Window) func() {
	controller.window = window
	return window.OnClose(func(event *mygo.CloseEvent) {
		event.PreventDefault()
		controller.RequestClose()
		window.Invalidate()
	})
}

// ShowSettings 应由主线程调用，例如托盘中的“打开设置”。
func (controller *Controller) ShowSettings() {
	controller.page, controller.settingsSection = "settings", 1
}

func (controller *Controller) ShowUpdates() {
	controller.page, controller.settingsSection = "settings", 4
}

// RequestClose 应由主线程调用；取消弹窗不会提交记住选择。
func (controller *Controller) RequestClose() {
	controller.view = controller.actions.View()
	behavior := controller.view.Settings.CloseBehavior
	if behavior == "exit" || behavior == "quit" || (behavior == "tray" && controller.view.TrayAvailable) {
		controller.actions.RequestClose(behavior, false)
		return
	}
	controller.rememberClose = false
	controller.closeChoice = "tray"
	if !controller.view.TrayAvailable {
		controller.closeChoice = "exit"
	}
	controller.openModal("close")
}

// Run 供没有自行创建 MyGo 窗口的入口使用；已管理窗口的主程序使用 New/Content/AttachWindow。
func Run(actions app.Actions) error {
	if actions == nil {
		return fmt.Errorf("ToolNest UI: actions 不能为空")
	}
	controller := New(actions)
	mygo.App.WhenReady(func() {
		window := mygo.NewWindow(controller.WindowOptions("ToolNest"))
		controller.AttachWindow(window)
	})
	return mygo.App.Run()
}

func (controller *Controller) busy() bool {
	return controller.pending != "" || controller.view.Busy != ""
}

func (controller *Controller) launch(name string, action func()) {
	if controller.busy() {
		return
	}
	controller.launchBackground(name, action)
}

func (controller *Controller) launchBackground(name string, action func()) {
	if controller.pending != "" {
		return
	}
	controller.pending, controller.localError = name, ""
	// 不把系统对话框、预览读取或业务调用放进绘制栈；后台只投递结果，不改 UI 状态。
	go func() {
		action()
		controller.events <- func() { controller.pending = "" }
	}()
}

func (controller *Controller) synchronize() {
	for {
		select {
		case apply := <-controller.events:
			apply()
		default:
			goto drained
		}
	}
drained:
	controller.view = controller.actions.View()
	if !controller.initialized {
		controller.draft, controller.base = controller.view.Settings, controller.view.Settings
		controller.initialized = true
	}
	controller.draft.SnapshotDir, controller.base.SnapshotDir = controller.view.Settings.SnapshotDir, controller.view.Settings.SnapshotDir
	controller.draft.OnboardingDone, controller.base.OnboardingDone = controller.view.Settings.OnboardingDone, controller.view.Settings.OnboardingDone
	// 轮询不能冲掉未保存的草稿；仅无本地编辑或服务确认保存后才同步。
	// 保存回执必须来自新的成功修订；托盘等无关错误不能挡住回执，密码保存失败也不能被误认成功。
	if controller.submitted != nil && controller.pending == "" && controller.view.Busy == "" && controller.view.SettingsRevision > controller.submittedRevision && controller.view.Settings == persistedSettings(*controller.submitted) {
		if controller.submittedSchedule {
			copySchedule(&controller.draft, controller.view.Settings)
		} else if controller.draft == *controller.submitted {
			controller.draft = controller.view.Settings
		}
		controller.base = controller.view.Settings
		controller.submitted = nil
	}
	if controller.draft == controller.base && controller.submitted == nil {
		controller.draft, controller.base = controller.view.Settings, controller.view.Settings
	}
	if !controller.view.Settings.OnboardingDone && !controller.onboardingShown && controller.modal == "" {
		controller.onboardingShown = true
		controller.onboarding = controller.view.Settings
		controller.openModal("onboarding")
	}
}

func (controller *Controller) render(context *native.Context) {
	ensureMisans()
	controller.synchronize()
	controller.shortcuts(context)
	context.After(250 * time.Millisecond)
	controller.systemDark = context.Theme().Dark
	context.SetTheme(controller.renderTheme())
	width, _ := context.Size()
	contentWidth := width - sidebarWidth(width)
	// 根背景由应用完整绘制，内部磨砂不再依赖系统是否允许透明窗口。
	ambientBackdrop(context)
	// 侧栏与工作面悬浮在同一环境底上；正文不再盖实底，否则毛玻璃只剩一圈装饰。
	native.Row(context.Key("shell")).Fill().Gap(0).AlignItems(native.Stretch).Children(func() {
		controller.sidebar(context, width)
		frostedSurface(context, native.Column(context.Key("workspace")).Grow(1).MinWidth(0).MinHeight(0).
			Margin(12, 12, 12, 0).Radius(20).Clip().Gap(0), false).Children(func() {
			controller.topbar(context, contentWidth)
			controller.feedback(context)
			native.Column(context.Key("workspace-content")).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				if controller.page == "home" {
					controller.homePage(context, contentWidth)
				} else {
					// 非首页仍让内容区占满可用高度，长表格自行滚动；内部面板按内容或表格单独决定高度。
					native.Column(context.Key(controller.page)).Grow(1).MinHeight(0).Padding(0, contentInset(contentWidth), 10, contentInset(contentWidth)).Gap(6).Children(func() {
						if controller.page == "settings" {
							controller.settingsPage(context)
							return
						}
						switch controller.tab {
						case 0:
							controller.currentPage(context, contentWidth)
						case 1:
							controller.snapshotsPage(context, contentWidth)
						case 2:
							controller.schedulePage(context)
						case 3:
							controller.records(context)
						}
					})
				}
			})
			controller.footer(context)
		})
	})
	controller.dialogs(context)
}

// renderTheme 从当前视图生成主题；独立成函数便于字号档位回归直接断言。
func (controller *Controller) renderTheme() *native.Theme {
	// 显式浅色/深色优先；跟随系统时沿用绘制上下文的明暗。
	source := native.LightTheme()
	if controller.systemDark {
		source = native.DarkTheme()
	}
	if controller.view.Settings.Theme == "light" {
		source = native.LightTheme()
	} else if controller.view.Settings.Theme == "dark" {
		source = native.DarkTheme()
	}
	theme := refinedTheme(source, controller.view.Settings)
	return &theme
}

func (controller *Controller) topbar(context *native.Context, width float32) {
	theme := context.Theme()
	bar := chromeSurface(context, native.Row(context.Key("topbar")).Height(52).
		Padding(8, contentInset(width)).Gap(8).AlignItems(native.Center).DragWindow())
	bar.Children(func() {
		title := "PATH 保护"
		if controller.tab == 1 {
			title = "快照"
		} else if controller.tab == 2 {
			title = "自动备份"
		} else if controller.tab == 3 {
			title = "活动记录"
		}
		if controller.page == "settings" {
			title = "设置"
		} else if controller.page == "home" {
			title = "概览"
		}
		native.Text(context, title).FontSize(pageTitleSize(context)).FontWeight(500).Grow(1).MinWidth(0).SingleLine().TextColor(theme.Text)
		if controller.page != "settings" && controller.page != "home" {
			refresh, backup := "刷新 PATH", "立即备份"
			if width < 580 {
				refresh, backup = "刷新", "备份"
			}
			if quietButton(context, refresh, controlOptions{key: "refresh", label: "刷新 PATH", icon: refreshIcon, disabled: controller.busy()}).Clicked() {
				controller.launch("读取 PATH", controller.actions.Refresh)
			}
			if primaryButton(context, backup, controlOptions{key: "backup", label: "立即备份", icon: backupIcon, disabled: controller.busy()}).Clicked() {
				controller.launch("创建快照", controller.actions.Backup)
			}
		}
		controller.captionButtons(context)
	})
}

// 同时只展开一条反馈；被覆盖的结果与更新保留底栏入口，不挤占主工作区。
func (controller *Controller) feedbackKind() string {
	switch {
	case controller.view.PendingExit != "":
		return "exit"
	case controller.localError != "" || len(controller.view.Problems) > 0 || controller.view.Error != "":
		return "error"
	case controller.updateMessage() != "" && !(controller.page == "settings" && controller.settingsSection == 4):
		return "update"
	default:
		return ""
	}
}

func (controller *Controller) feedback(context *native.Context) {
	switch controller.feedbackKind() {
	case "exit":
		controller.exitNotice(context)
	case "error":
		controller.statusbar(context)
	case "update":
		controller.updateNotice(context)
	}
}

func (controller *Controller) footer(context *native.Context) {
	kind := controller.feedbackKind()
	update := kind != "update" && controller.updateMessage() != "" && !(controller.page == "settings" && controller.settingsSection == 4)
	result := controller.view.Result.ID != "" && controller.view.Result.ID != controller.dismissedResult
	duplicateResultStatus := false
	err := controller.view.Error
	if controller.localError != "" {
		err = controller.localError
	}
	hiddenError := kind != "error" && (err != "" || len(controller.view.Problems) > 0)
	draft := controller.draft != controller.base && controller.page != "settings" && !(controller.page == "path" && controller.tab == 2)
	status := controller.view.Busy
	if controller.pending != "" {
		status = controller.pending
	}
	if status == "" && controller.view.Status != "就绪" {
		status = controller.view.Status
		previousResult := controller.view.Result
		// 仅去掉同一次备份／恢复的重复回执，后续连接测试和设置保存仍需明确反馈。
		if previousResult.ID != "" && (status == previousResult.Title || status == previousResult.Message || previousResult.Operation != "" && strings.HasPrefix(status, previousResult.Operation)) {
			status = ""
			duplicateResultStatus = true
		}
	}
	if status == "" && !update && !result && !hiddenError && !draft {
		return
	}
	width, _ := context.Size()
	chromeSurface(context, native.Row(context.Key("footer")).FillWidth().MinHeight(30).
		Padding(3, contentInset(width-sidebarWidth(width))).Gap(6).Wrap().BorderWidth(1, 0, 0, 0)).Children(func() {
		if status != "" {
			native.Text(context, status).FontSize(detailSize(context)).TextColor(context.Theme().TextMuted).SingleLine().Grow(1).MinWidth(0).Role(native.RoleStatus)
		} else if result && duplicateResultStatus && kind != "error" {
			// 只有业务状态是同一次操作的重复回执时，才在状态栏保留结果标题；
			// 错误优先级和后续连接测试回执都不会被旧结果文字挤掉。
			native.Text(context, controller.view.Result.Title).FontSize(detailSize(context)).TextColor(context.Theme().Text).SingleLine().Grow(1).MinWidth(0).Role(native.RoleStatus).Tooltip(controller.view.Result.Message)
		} else {
			native.Spacer(context)
		}
		if hiddenError {
			if quietButton(context, "错误详情", controlOptions{icon: shieldIcon, textColor: context.Theme().Warning}).Clicked() {
				if len(controller.view.Problems) > 0 {
					problem := controller.view.Problems[0]
					err = problem.Title + "\n" + problem.Message + "\n" + problem.Details
				}
				controller.showText("错误详情", err)
			}
		}
		if result {
			if quietButton(context, "结果详情", controlOptions{icon: eyeIcon}).Clicked() {
				controller.resultShown = controller.view.Result
				controller.openModal("operation-result")
			}
			if resultSnapshot := controller.view.Result; resultSnapshot.SnapshotID != "" && resultSnapshot.Outcome == "success" {
				label := "查看本次快照"
				if resultSnapshot.Operation == "恢复 PATH" {
					label = "查看恢复结果"
				}
				if quietButton(context, label, controlOptions{key: "footer-result-snapshot", icon: archiveIcon}).Clicked() {
					if resultSnapshot.Operation == "恢复 PATH" {
						controller.resultShown = resultSnapshot
						controller.openModal("operation-result")
					} else {
						controller.openSnapshot(resultSnapshot.SnapshotID)
					}
				}
			}
		}
		if update {
			if quietButton(context, "查看更新", controlOptions{icon: updateIcon}).Clicked() {
				controller.ShowUpdates()
			}
		}
		if draft {
			if quietButton(context, "设置未保存", controlOptions{}).Clicked() {
				controller.ShowSettings()
			}
		}
		if result {
			if quietButton(context, "收起", controlOptions{label: "收起操作结果"}).Clicked() {
				controller.dismissedResult = controller.view.Result.ID
			}
		}
	})
}

func (controller *Controller) statusbar(context *native.Context) {
	if controller.localError == "" && len(controller.view.Problems) > 0 {
		problem := controller.view.Problems[0]
		noticePanel(context, "actionable-problem").Children(func() {
			native.Column(context).MinWidth(0).Gap(4).Children(func() {
				native.Text(context, problem.Title).FontSize(controlTextSize(context)).FontWeight(600).Role(native.RoleAlert)
				native.Text(context, problem.Message).FontSize(detailSize(context)).TextColor(context.Theme().TextMuted).SingleLine().Tooltip(problem.Message)
			})
			native.Row(context).Gap(8).Wrap().Children(func() {
				label := "重新读取"
				if problem.Action == "data" {
					label = "检查数据目录"
				}
				if secondaryButton(context, label, controlOptions{key: "problem-action", height: 34, disabled: controller.busy()}).Clicked() {
					if problem.Action == "data" {
						controller.page, controller.settingsSection = "settings", 3
					} else {
						controller.launch("重新读取", controller.actions.Refresh)
					}
				}
				if secondaryButton(context, "查看原因", controlOptions{key: "problem-detail", icon: eyeIcon, height: 34}).Clicked() {
					controller.showText(problem.Title, problem.Message+"\n\n"+problem.Details)
				}
			})
		})
		return
	}
	err := controller.view.Error
	if controller.localError != "" {
		err = controller.localError
	}
	if err != "" {
		noticePanel(context, "error").Row().Gap(8).Children(func() {
			native.Text(context, "!").FontWeight(600).TextColor(context.Theme().Warning).Width(12)
			native.Text(context, errorSummary(err)).Role(native.RoleAlert).FontSize(detailSize(context)).TextColor(context.Theme().TextMuted).SingleLine().Grow(1).MinWidth(0)
			if secondaryButton(context, "错误详情", controlOptions{key: "error-detail", icon: eyeIcon, height: 32}).Clicked() {
				controller.showText("错误详情", err)
			}
		})
	}
}

func (controller *Controller) openModal(kind string) {
	controller.modal, controller.modalOpen, controller.modalError = kind, true, ""
}

func (controller *Controller) showText(title, text string) {
	controller.textTitle, controller.textDetail = title, text
	controller.openModal("text")
}

var pathIcon = func() *native.SVG {
	icon, _ := native.ParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 9 3 3-3 3m6 0h4"/></svg>`))
	return icon
}()
