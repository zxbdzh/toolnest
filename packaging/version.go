// Package packaging owns release identity shared by the app and build scripts.
package packaging

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var version string

// 这里只链接公开信息；生产私钥从不进入程序或构建参数。
var Repository string
var UpdatePublicKey string
var BuildChannel = "development"

func Version() string { return strings.TrimSpace(version) }
