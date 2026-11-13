// ToolNest-maintenance is a restricted installer helper, never a process killer.
package main

import (
	"flag"
	"fmt"
	"os"
	lifecycle "toolnest/packaging/runtime"
)

func main() {
	mode := flag.String("mode", "", "check-install, check-uninstall, remove-owned-run")
	dir := flag.String("install-dir", "", "fixed per-user installation directory")
	flag.Parse()
	if flag.NArg() != 0 {
		os.Exit(2)
	}
	var err error
	switch *mode {
	case "check-install", "check-uninstall":
		err = lifecycle.ValidateExisting(*dir)
		if err == nil {
			err = lifecycle.CheckIdle(*dir)
		}
	case "remove-owned-run":
		_, err = lifecycle.RemoveOwnedRun(*dir)
	default:
		err = fmt.Errorf("unsupported maintenance operation")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
