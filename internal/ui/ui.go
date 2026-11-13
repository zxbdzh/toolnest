// Package ui 实现 ToolNest 的 MyGo 原生界面，系统操作仅通过 app.Actions 提交。
package ui

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
	native "github.com/egoist/mygo/ui"

	"toolnest/internal/app"
)

// Controller 的状态归 UI 线程所有；New 不启动窗口、托盘或后台业务服务。
type Controller struct {
	actions            app.Actions
	view               app.View
	events             chan func()
	pending            string
	localError         string
	page               string
	homeQuery          string
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

// AttachWindow 接入关闭确认。主代理仍负责实现 RequestClose、托盘与安全退出。
func (controller *Controller) AttachWindow(window *mygo.Window) func() {
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
		window := mygo.NewWindow(mygo.WindowOptions{
			Title: "ToolNest", Width: 1180, Height: 780,
			MinWidth: 620, MinHeight: 520, Vibrancy: mygo.VibrancySidebar,
			Content: controller.Content(),
		})
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
	controller.synchronize()
	context.After(250 * time.Millisecond)
	theme := *context.Theme()
	switch controller.view.Settings.Theme {
	case "light":
		theme = *native.LightTheme()
	case "dark":
		theme = *native.DarkTheme()
	}
	theme = refinedTheme(&theme)
	context.SetTheme(&theme)
	width, _ := context.Size()
	if context.Vibrancy() {
		context.Root().Background(native.Transparent)
	}
	shell := native.Column(context.Key("shell")).Fill().Background(theme.Background)
	if context.Vibrancy() {
		// 壳层必须透明，否则顶栏的半透明材质只会叠在本应用的实底上。
		shell.Background(native.Transparent)
	}
	shell.Children(func() {
		controller.header(context, width)
		native.Column(context.Key("workspace")).Grow(1).MinHeight(0).FillWidth().Background(theme.Background).Children(func() {
			controller.updateNotice(context)
			controller.operationNotice(context)
			if controller.page == "home" {
				controller.homePage(context, width)
			} else {
				controller.topbar(context, width)
				native.Column(context.Key(controller.page)).Grow(1).MinHeight(0).Padding(0, 24, 16, 24).Gap(14).Children(func() {
					if controller.page == "settings" {
						controller.settingsPage(context)
						return
					}
					switch controller.tab {
					case 0:
						controller.currentPage(context, width)
					case 1:
						controller.snapshotsPage(context, width)
					case 2:
						controller.schedulePage(context)
					}
				})
			}
			if controller.page != "settings" {
				controller.statusbar(context)
			}
			controller.exitNotice(context)
			controller.footer(context)
		})
	})
	controller.dialogs(context)
}

func (controller *Controller) header(context *native.Context, width float32) {
	theme := context.Theme()
	bar := native.Row(context.Key("top-navigation")).FillWidth().Height(76).Padding(0, 28).Gap(16)
	if context.Vibrancy() {
		bar.Background(theme.Surface.Alpha(0.84))
	} else {
		bar.Background(panelColor(theme))
	}
	bar.Children(func() {
		brand := native.Button(context.Key("brand"), "").Label("返回工具空间").Border(0, native.Transparent).Background(native.Transparent).Padding(0).Gap(10)
		brand.Children(func() {
			native.Row(context).Width(36).Height(36).Justify(native.Center).Background(theme.Accent).Radius(11).Children(func() {
				native.Icon(context, nestIcon).Width(24).Height(24).TextColor(theme.AccentText)
			})
			native.Column(context).Gap(2).Children(func() {
				native.Text(context, "ToolNest").Font("Segoe UI").FontSize(18).FontWeight(600)
				if width >= 780 {
					label := "你的桌面工具空间"
					if controller.view.Isolated {
						label = "更新测试版 · 使用合成 PATH"
					}
					native.Text(context, label).FontSize(11).TextColor(theme.TextMuted)
				}
			})
		})
		if brand.Clicked() {
			controller.page = "home"
		}
		native.Spacer(context)
		// 复用原生按钮的焦点、键盘和可访问性；胶囊只改变外观，不手写点击区域。
		native.Row(context.Key("navigation")).Gap(3).Padding(4).Radius(22).Background(theme.Surface).Children(func() {
			for _, item := range []struct {
				label, page string
				tab         int
			}{{"工具空间", "home", 0}, {"PATH 保护", "path", 0}, {"快照", "path", 1}, {"自动备份", "path", 2}} {
				selected := controller.page == item.page && (item.page == "home" || controller.tab == item.tab)
				button := native.Button(context.Key(item.label), item.label).Height(34).FontSize(13).Padding(0, 14).Radius(17).Border(0, native.Transparent)
				if width < 780 {
					button.Padding(0, 8).FontSize(11)
				}
				if selected {
					button.Background(panelColor(theme)).TextColor(mistLink(theme)).FontWeight(600).Shadow(0, 1, 3, 0, native.Hex("#203a50").Alpha(0.08))
				} else {
					button.Background(native.Transparent).TextColor(theme.TextMuted)
				}
				if button.Clicked() {
					controller.page, controller.tab = item.page, item.tab
				}
			}
		})
		native.Spacer(context)
		settings := native.Button(context.Key("settings-nav"), "").Label("应用设置").Tooltip("应用设置").Width(40).Height(40).Border(0, native.Transparent).Padding(10).Background(native.Transparent)
		if controller.page == "settings" {
			settings.Background(theme.SurfaceHover)
		}
		settings.Children(func() {
			native.Icon(context, settingsIcon).Width(18).Height(18).TextColor(mistLink(theme))
		})
		if settings.Clicked() {
			controller.ShowSettings()
		}
	})
}

func (controller *Controller) topbar(context *native.Context, width float32) {
	theme := context.Theme()
	bar := native.Row(context.Key("topbar")).Height(96).Padding(16, 26).Gap(8)
	_, height := context.Size()
	compact := height < 640
	if compact {
		bar.Height(72).Padding(12, 24)
	}
	if context.Vibrancy() {
		bar.Background(theme.Background.Alpha(0.92))
	} else {
		bar.Background(theme.Background)
	}
	bar.Children(func() {
		title, description := "PATH 保护", "只读查看注册表原值 · 备份用户与系统两个范围"
		if controller.tab == 1 {
			title, description = "快照时光机", "留住每一个好状态 · 比较后再恢复"
		} else if controller.tab == 2 {
			title, description = "自动备份", "有变化时留一份快照，没有变化时安静守候"
		}
		if controller.page == "settings" {
			title, description = "应用设置", "让 ToolNest 按你的习惯工作"
		}
		if width < 780 && controller.page != "settings" {
			description = "只读查看 · 保留原始内容"
		}
		native.Column(context).Grow(1).MinWidth(0).Gap(6).Children(func() {
			native.Text(context, title).FontSize(24).FontWeight(600).SingleLine()
			if !compact {
				native.Text(context, description).FontSize(13).TextColor(theme.TextMuted).SingleLine()
			}
		})
		if controller.page == "settings" {
			return
		}
		if controller.tab != 0 {
			if native.Button(context.Key("back-current"), "当前 PATH").Height(34).FontSize(12).Border(0, native.Transparent).Clicked() {
				controller.tab = 0
			}
		}
		refresh, backup := "刷新 PATH", "立即备份"
		if width < 780 {
			refresh, backup = "刷新", "备份"
		}
		if native.Button(context.Key("refresh"), refresh).Height(36).Padding(0, 14).Border(0, native.Transparent).Disabled(controller.busy()).Clicked() {
			controller.launch("读取 PATH", controller.actions.Refresh)
		}
		if native.PrimaryButton(context.Key("backup"), backup).Height(36).Padding(0, 16).Disabled(controller.busy()).Clicked() {
			controller.launch("创建快照", controller.actions.Backup)
		}
	})
}

func (controller *Controller) statusbar(context *native.Context) {
	if len(controller.view.Problems) > 0 {
		problem := controller.view.Problems[0]
		native.Row(context.Key("actionable-problem")).Margin(0, 24, 8, 24).Padding(10, 12).Gap(10).Radius(8).Background(context.Theme().Surface).Children(func() {
			native.Column(context).Grow(1).MinWidth(0).Gap(4).Children(func() {
				native.Text(context, problem.Title).FontSize(13).FontWeight(600).Role(native.RoleAlert)
				native.Text(context, problem.Message).FontSize(12).TextColor(context.Theme().TextMuted)
			})
			label := "重新读取"
			if problem.Action == "data" {
				label = "检查数据目录"
			}
			if native.Button(context, label).Height(32).Disabled(controller.busy()).Clicked() {
				if problem.Action == "data" {
					controller.page, controller.settingsSection = "settings", 3
				} else {
					controller.launch("重新读取", controller.actions.Refresh)
				}
			}
			if native.Button(context, "查看原因").Height(32).Clicked() {
				controller.showText(problem.Title, problem.Message+"\n\n"+problem.Details)
			}
		})
	}
	status := controller.view.Status
	if controller.view.Busy != "" {
		status = controller.view.Busy
	}
	if controller.pending != "" {
		status = controller.pending
	}
	err := controller.view.Error
	if controller.localError != "" {
		err = controller.localError
	}
	if err != "" && len(controller.view.Problems) == 0 {
		native.Row(context.Key("error")).Margin(0, 24, 12, 24).Padding(6, 12).Gap(8).Radius(8).Background(context.Theme().Surface).Children(func() {
			native.Text(context, "!").FontWeight(600).TextColor(context.Theme().Warning).Width(12)
			native.Text(context, errorSummary(err)).Role(native.RoleAlert).FontSize(12).TextColor(context.Theme().TextMuted).SingleLine().Grow(1).MinWidth(0)
			if native.Button(context, "错误详情").Height(28).FontSize(12).Border(0, native.Transparent).Clicked() {
				controller.showText("错误详情", err)
			}
		})
	}
	if status != "" && !(status == "就绪" && err != "") {
		native.Text(context.Key("status"), status).Role(native.RoleStatus).FontSize(11).TextColor(context.Theme().TextMuted).Padding(0, 24, 10, 24).SingleLine()
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
