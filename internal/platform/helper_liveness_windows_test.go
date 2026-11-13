//go:build windows

package platform

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"testing"
	"time"
	"toolnest/internal/core"
)

func TestHelperPipeCannotImpersonateElevatedClient(t *testing.T) {
	if helperPipeClientFlags&windows.SECURITY_SQOS_PRESENT == 0 {
		t.Fatal("助手管道未显式指定身份模拟上限")
	}
	// SECURITY_IDENTIFICATION=0x10000；级别掩码覆盖 impersonation/delegation。
	if helperPipeClientFlags&0x00030000 != windows.SECURITY_IDENTIFICATION {
		t.Fatal("助手允许普通父进程模拟管理员身份")
	}
}

func TestCallerBoundStoreRevokesAfterCallerExit(t *testing.T) {
	// 可等待事件模拟进程退出，不启动子进程、不请求 UAC，也不写真实注册表。
	h, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	memory := &memoryStore{state: sampleState()}
	store := &callerBoundStore{rawStore: memory, parent: h}
	if _, err := store.read(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := windows.SetEvent(h); err != nil {
		t.Fatal(err)
	}
	if _, err := store.read(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	if err := store.write(context.Background(), core.System, sampleState().System); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	if memory.writes != 0 {
		t.Fatal("调用者退出后仍写入")
	}
}
func TestCallerWatcherCancelsAbandonedSession(t *testing.T) {
	h, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); watchCaller(ctx, cancel, h) }()
	if err := windows.SetEvent(h); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("调用者退出未撤销会话")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal(ctx.Err())
	}
}
