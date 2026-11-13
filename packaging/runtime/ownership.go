// Package runtime supplies the installation lifecycle contract for main.
package runtime

import (
	"errors"
	"path/filepath"
	"strings"
)

const RunName = "ToolNest"
const RunArguments = "--autostart"
const InstallMarker = "ToolNest.per-user.v1"

var ErrBusy = errors.New("ToolNest is running or performing a critical operation; exit normally and retry")

type RunValue struct {
	Name    string
	Type    uint32
	Command string
}

func OwnedRun(value RunValue, executable string) bool {
	if value.Name != RunName || value.Type != 1 || !filepath.IsAbs(executable) {
		return false
	}
	// 只匹配本应用写出的 REG_SZ、完整绝对路径和完整参数，不展开变量或猜测归属。
	suffix := `" ` + RunArguments
	quotedSuffix := `" "` + RunArguments + `"`
	if strings.HasSuffix(value.Command, quotedSuffix) {
		suffix = quotedSuffix
	}
	if !strings.HasPrefix(value.Command, `"`) || !strings.HasSuffix(value.Command, suffix) {
		return false
	}
	path := strings.TrimSuffix(strings.TrimPrefix(value.Command, `"`), suffix)
	// Windows 路径大小写不敏感，命令参数却必须精确匹配，不能把 --AUTOSTART 当作已归属。
	return strings.EqualFold(path, filepath.Clean(executable))
}

func RunCommand(executable string) (string, error) {
	if !filepath.IsAbs(executable) || strings.ContainsAny(executable, "\"\r\n\x00") {
		return "", errors.New("absolute executable path required")
	}
	return `"` + filepath.Clean(executable) + `" ` + RunArguments, nil
}
