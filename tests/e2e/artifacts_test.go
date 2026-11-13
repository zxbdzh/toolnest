package e2e

import (
	"bytes"
	"context"
	"debug/pe"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"toolnest/internal/update"
	"toolnest/tests/e2e/lab"
)

func TestOptionalActualSignedApplicationStaging(t *testing.T) {
	directory := os.Getenv("TOOLNEST_ACCEPTANCE_SIGNED_UPDATE")
	if directory == "" {
		t.Skip("actual signed application fixture not supplied; installed AT-18 remains blocked")
	}
	archive, err := os.ReadFile(filepath.Join(directory, "update-0.1.1.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := os.ReadFile(filepath.Join(directory, "update-0.1.1.sig"))
	if err != nil {
		t.Fatal(err)
	}
	public, err := os.ReadFile(filepath.Join(directory, "update.pub"))
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"http", "socks5"} {
		t.Run(protocol, func(t *testing.T) {
			environment, err := lab.StartSigned(archive, strings.TrimSpace(string(signature)), strings.TrimSpace(string(public)))
			if err != nil {
				t.Fatal(err)
			}
			defer environment.Close()
			address := environment.HTTPProxy
			if protocol == "socks5" {
				address = environment.SOCKSProxy
			}
			updater, _, install := updateManager(t, environment, "custom", address, true)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err = updater.Check(ctx, true); err != nil {
				t.Fatal(err)
			}
			prepared, err := updater.Prepare(ctx)
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(prepared.Executable)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(content, []byte("MZ")) || len(content) < 1024*1024 {
				t.Fatal("expected actual built application PE, not synthetic payload")
			}
			image, err := pe.NewFile(bytes.NewReader(content))
			if err != nil {
				t.Fatal(err)
			}
			defer image.Close()
			var subsystem uint16
			switch header := image.OptionalHeader.(type) {
			case *pe.OptionalHeader32:
				subsystem = header.Subsystem
			case *pe.OptionalHeader64:
				subsystem = header.Subsystem
			}
			if subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
				t.Fatal("signed application must use Windows GUI subsystem; console builds open an unwanted terminal")
			}
			old, err := os.ReadFile(filepath.Join(install, "ToolNest.exe"))
			if err != nil || string(old) != "MZ-SYNTHETIC-0.1.0" {
				t.Fatal("staging replaced installed program without consent")
			}
			if updater.Status().Phase != update.Ready || environment.LeakedCredentials.Load() {
				t.Fatal("staging/credential invariant failed")
			}
			// 客体安装未执行时，只记录真实产物的下载与验签，不声称已安装升级成功。
			detail, _ := json.Marshal(map[string]any{"protocol": protocol, "phase": updater.Status().Phase, "bytes": len(content), "scope": "actual PE signed download and staging; not installed lifecycle"})
			t.Log(string(detail))
		})
	}
}
