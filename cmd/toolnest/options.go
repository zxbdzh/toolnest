package toolnest

import (
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"path/filepath"
	"strings"
)

type options struct {
	dataDir, testRoot, testSource, testKeyFile          string
	helperFile, helperToken, helperTarget, confirmToken string
	legacyExecutables                                   paths
	autostart, resumeTray, version, buildInfo           bool
	diagnostics, smoke                                  bool
}

type paths []string

func (values *paths) String() string { return strings.Join(*values, ";") }
func (values *paths) Set(value string) error {
	if !filepath.IsAbs(value) || !strings.EqualFold(filepath.Ext(value), ".exe") {
		return errors.New("旧入口归属依据必须是已知旧版 exe 的绝对路径")
	}
	*values = append(*values, filepath.Clean(value))
	return nil
}

func parseOptions(arguments []string, output io.Writer) (options, error) {
	var result options
	flags := flag.NewFlagSet("ToolNest", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&result.dataDir, "data-dir", "", "隔离验收数据目录；必须同时指定 --test-root")
	flags.StringVar(&result.testRoot, "test-root", "", "32 位小写十六进制平台隔离标识；必须同时指定 --data-dir")
	flags.StringVar(&result.testSource, "test-update-source", "", "仅隔离测试可使用的数值 loopback HTTP 更新 manifest")
	flags.StringVar(&result.testKeyFile, "test-update-key-file", "", "仅隔离测试使用的 base64 Ed25519 公钥文件")
	flags.Var(&result.legacyExecutables, "legacy-executable", "明确已知的旧版可执行文件归属依据，可重复；不自动停用")
	flags.BoolVar(&result.autostart, "autostart", false, "由本应用的当前用户自启入口启动")
	flags.BoolVar(&result.resumeTray, "resume-tray", false, "更新后恢复已隐藏窗口，仅配合启动确认令牌")
	flags.BoolVar(&result.version, "version", false, "输出构建版本，不初始化桌面或数据")
	flags.BoolVar(&result.buildInfo, "toolnest-build-info", false, "输出安装器所需的只读构建标识")
	flags.BoolVar(&result.diagnostics, "diagnostics-json", false, "输出只读启动诊断 JSON，不启动窗口或计划")
	flags.BoolVar(&result.smoke, "smoke", false, "仅隔离测试：打开原生窗口并安全退出，输出 JSON")
	flags.StringVar(&result.helperFile, "toolnest-update-helper", "", "内部更新助手请求文件")
	flags.StringVar(&result.helperTarget, "toolnest-update-target", "", "内部更新助手的原始程序路径")
	flags.StringVar(&result.confirmToken, "toolnest-update-confirm", "", "内部新版启动确认令牌")
	if err := flags.Parse(arguments); err != nil {
		return result, err
	}
	if result.helperFile != "" && len(flags.Args()) == 1 {
		result.helperToken = flags.Args()[0]
	} else if len(flags.Args()) != 0 {
		return result, errors.New("不接受位置参数或任意注册表路径")
	}
	if (result.testRoot == "") != (result.dataDir == "") {
		return result, errors.New("--data-dir 与 --test-root 必须严格配套，正常运行使用固定用户目录")
	}
	if result.testing() {
		if !testID(result.testRoot) || !filepath.IsAbs(result.dataDir) {
			return result, errors.New("--test-root 必须是 32 位小写十六进制，--data-dir 必须为绝对路径")
		}
		result.dataDir = filepath.Clean(result.dataDir)
	}
	if (result.testSource == "") != (result.testKeyFile == "") || ((!result.testing()) && (result.testSource != "" || result.smoke)) {
		return result, errors.New("测试更新源与公钥须同时指定，且与 --smoke 一样只允许显式隔离模式")
	}
	if result.testKeyFile != "" && !filepath.IsAbs(result.testKeyFile) {
		return result, errors.New("测试公钥文件必须使用绝对路径")
	}
	if (result.helperFile != "" && (!tokenID(result.helperToken) || !filepath.IsAbs(result.helperFile))) || (result.confirmToken != "" && !tokenID(result.confirmToken)) {
		return result, errors.New("内部更新参数无效")
	}
	if result.helperFile != "" && result.confirmToken != "" {
		return result, errors.New("更新助手与新版确认模式互斥")
	}
	if (result.helperTarget != "" && (result.helperFile == "" || !filepath.IsAbs(result.helperTarget))) || (result.helperFile != "" && result.helperTarget == "") {
		return result, errors.New("更新助手必须提供配套原始程序路径")
	}
	if result.resumeTray && result.confirmToken == "" && result.helperFile == "" {
		return result, errors.New("--resume-tray 仅允许更新监督启动")
	}
	if (result.helperFile != "" || result.confirmToken != "") && (result.version || result.buildInfo || result.diagnostics) || (result.helperFile != "" && result.smoke) {
		return result, errors.New("更新内部模式不能与诊断或 smoke 混用")
	}
	return result, nil
}

func (value options) testing() bool { return value.testRoot != "" }
func testID(value string) bool      { return len(value) == 32 && lowerHex(value) }
func tokenID(value string) bool     { return len(value) == 64 && lowerHex(value) }
func lowerHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func within(parent, child string) bool {
	relative, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (value options) runtimeArguments() []string {
	var result []string
	if value.testing() {
		result = append(result, "--data-dir", value.dataDir, "--test-root", value.testRoot)
		if value.testSource != "" {
			result = append(result, "--test-update-source", value.testSource, "--test-update-key-file", value.testKeyFile)
		}
	}
	for _, path := range value.legacyExecutables {
		result = append(result, "--legacy-executable", path)
	}
	return result
}
