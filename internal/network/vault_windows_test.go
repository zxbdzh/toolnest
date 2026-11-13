package network

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestWindowsCredentialManagerIsolated(t *testing.T) {
	// 使用唯一测试命名空间，绝不访问或删除用户已有代理凭据。
	vault := WindowsVault{Namespace: fmt.Sprintf("ToolNest-Test-%d", time.Now().UnixNano())}
	ctx := context.Background()
	t.Cleanup(func() { _ = vault.Delete(ctx, "test") })
	want := Credentials{Username: "测试账号", Password: "仅测试-password-🔐"}
	if err := vault.Write(ctx, "test", want); err != nil {
		t.Fatalf("Credential Manager write failed: %v", err)
	}
	got, err := vault.Read(ctx, "test")
	if err != nil || got != want {
		t.Fatalf("Credential Manager round trip failed: %v", err)
	}
	if err := vault.Delete(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Read(ctx, "test"); err == nil {
		t.Fatal("deleted credential remained")
	}
}
