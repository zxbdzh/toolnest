package main

import (
	"os"

	"toolnest/cmd/toolnest"
)

func main() {
	os.Exit(toolnest.Run(os.Args[1:], os.Stdout, os.Stderr))
}
