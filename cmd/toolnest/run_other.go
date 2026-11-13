//go:build !windows

package toolnest

import (
	"fmt"
	"io"

	"toolnest/packaging"
)

func Run(arguments []string, stdout, stderr io.Writer) int {
	flags, err := parseOptions(arguments, stderr)
	if err != nil {
		return 2
	}
	if flags.buildInfo {
		if packaging.WriteBuildInfo(stdout) != nil {
			return 1
		}
		return 0
	}
	if flags.version {
		fmt.Fprintln(stdout, packaging.Version())
		return 0
	}
	fmt.Fprintln(stderr, "ToolNest 原生入口仅支持 Windows 11 x64")
	return 1
}
