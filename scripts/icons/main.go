package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"toolnest/internal/brand"
)

func main() {
	directory := flag.String("out", "", "new external asset output directory")
	flag.Parse()
	if !filepath.IsAbs(*directory) {
		fmt.Fprintln(os.Stderr, "absolute asset directory required")
		os.Exit(1)
	}
	if err := os.MkdirAll(*directory, 0700); err != nil {
		panic(err)
	}
	for name, data := range map[string][]byte{"ToolNest.ico": brand.ICO(), "ToolNest.png": brand.PNG(256, false), "ToolNest-tray.png": brand.PNG(32, false)} {
		file, err := os.OpenFile(filepath.Join(*directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			panic(err)
		}
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			panic(fmt.Sprint(writeErr, closeErr))
		}
	}
}
