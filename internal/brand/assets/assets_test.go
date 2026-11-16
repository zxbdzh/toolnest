package assets

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 内嵌字重必须能被 MyGo 实际注册；只检查字节存在会漏掉损坏的 TTF。
func TestMiSansFacesRegister(t *testing.T) {
	fonts, err := MiSans()
	if err != nil {
		t.Fatal(err)
	}
	if len(fonts) != 3 {
		t.Fatalf("expected three embedded faces, got %d", len(fonts))
	}
	for name, data := range fonts {
		if len(data) == 0 {
			t.Fatalf("%s is empty", name)
		}
		if err := ui.RegisterFont(data, "ToolNest MiSans Test "+name); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
}
