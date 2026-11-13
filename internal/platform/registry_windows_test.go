//go:build windows

package platform

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"os"
	"syscall"
	"testing"
	"time"
	"toolnest/internal/core"
)

func isolatedRegistry(t *testing.T) (*Registry, *windowsStore) {
	t.Helper()
	id := core.NewID()
	r, err := NewRegistry(Options{TestRoot: id})
	if err != nil {
		t.Fatal(err)
	}
	parent := testPrefix + id
	for _, scope := range []core.Scope{core.User, core.System} {
		key, _, err := registry.CreateKey(registry.CURRENT_USER, parent+`\`+string(scope), registry.ALL_ACCESS|registry.WOW64_64KEY)
		if err != nil {
			t.Fatal(err)
		}
		key.Close()
	}
	t.Cleanup(func() {
		for _, scope := range []core.Scope{core.User, core.System} {
			if err := registry.DeleteKey(registry.CURRENT_USER, parent+`\`+string(scope)); err != nil {
				t.Error(err)
			}
		}
		if err := registry.DeleteKey(registry.CURRENT_USER, parent); err != nil {
			t.Error(err)
		}
	})
	return r, r.store.(*windowsStore)
}
func TestWindowsReadDoesNotCreateMissingKey(t *testing.T) {
	id := core.NewID()
	r, err := NewRegistry(Options{TestRoot: id})
	if err != nil {
		t.Fatal(err)
	}
	state, err := r.Read(context.Background())
	if err != nil || state.User.Exists || state.System.Exists {
		t.Fatal(state, err)
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, testPrefix+id, registry.READ)
	if err == nil {
		key.Close()
		t.Fatal("Read created a key")
	}
	if !errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		t.Fatal(err)
	}
}
func TestWindowsRawRoundTripAndOnlyPath(t *testing.T) {
	r, store := isolatedRegistry(t)
	ctx := context.Background()
	root, path, _ := store.location(core.User)
	key, err := registry.OpenKey(root, path, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	if err = key.SetStringValue("Unrelated", "preserve me"); err != nil {
		t.Fatal(err)
	}
	cases := []core.Value{
		{Exists: true, Type: 1, Bytes: []byte{}},
		{Exists: true, Type: 1, Bytes: []byte{0, 0}},
		{Exists: true, Type: 2, Bytes: []byte{67, 0}},
		{Exists: true, Type: 2, Bytes: []byte{37, 0, 65, 0, 37, 0, 59, 0, 59, 0, 0, 0}},
		{Exists: true, Type: 1, Bytes: []byte{0, 0xd8, 0xff}},
		{},
	}
	for i, value := range cases {
		if err = store.write(ctx, core.User, value); err != nil {
			t.Fatal(i, err)
		}
		state, err := r.Read(ctx)
		if err != nil || !state.User.Equal(value) {
			t.Fatalf("case %d: got %#v want %#v: %v", i, state.User, value, err)
		}
		text, _, err := key.GetStringValue("Unrelated")
		if err != nil || text != "preserve me" {
			t.Fatal(text, err)
		}
	}
	if err = r.Notify(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestWindowsIsolatedRestoreAndProtection(t *testing.T) {
	r, raw := isolatedRegistry(t)
	ctx := context.Background()
	before := sampleState()
	if err := raw.write(ctx, core.User, before.User); err != nil {
		t.Fatal(err)
	}
	if err := raw.write(ctx, core.System, before.System); err != nil {
		t.Fatal(err)
	}
	plan := samplePlan(before, core.User, core.System)
	if err := r.ConfirmRestore(plan); err != nil {
		t.Fatal(err)
	}
	release, err := r.Authorize(ctx, plan.Scopes)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range plan.Scopes {
		if err = r.Write(ctx, scope, plan.Target.State.Value(scope)); err != nil {
			t.Fatal(err)
		}
	}
	actual, err := r.Read(ctx)
	if err != nil || !actual.Equal(plan.Target.State) {
		t.Fatal(actual, err)
	}
	for _, scope := range []core.Scope{core.System, core.User} {
		if err = r.Write(ctx, scope, before.Value(scope)); err != nil {
			t.Fatal(err)
		}
	}
	release()
	actual, err = r.Read(ctx)
	if err != nil || !actual.Equal(before) {
		t.Fatal(actual, err)
	}
}
func TestHelperArgumentsFailClosedWithoutElevation(t *testing.T) {
	for _, args := range [][]string{{helperFlag}, {helperFlag, "--command", "cmd.exe"}, {helperFlag, "--pipe", `\\remote\pipe\evil`, "--caller", "1", "--digest", "a"}} {
		handled, code, err := HandleHelper(context.Background(), args)
		if !handled || code == 0 || err == nil {
			t.Fatal(handled, code, err)
		}
	}
	handled, code, err := HandleHelper(context.Background(), []string{"--ordinary"})
	if handled || code != 0 || err != nil {
		t.Fatal(handled, code, err)
	}
}
func TestNativePipeFraming(t *testing.T) {
	// 只测试本进程两端，不启动 UAC、不触碰真实 PATH。
	name, _ := windows.UTF16PtrFromString(pipePrefix + core.NewID() + core.NewID())
	server, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE, windows.PIPE_TYPE_BYTE|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 65536, 65536, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s := &pipeStream{handle: server, ctx: ctx}
	defer s.close()
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(event)
	ov := windows.Overlapped{HEvent: event}
	err = windows.ConnectNamedPipe(server, &ov)
	if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
		t.Fatal(err)
	}
	client, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := &pipeStream{handle: client, ctx: ctx}
	defer c.close()
	var n uint32
	if err = waitOverlapped(ctx, server, &ov, &n); err != nil {
		t.Fatal(err)
	}
	var pid uint32
	if err = windows.GetNamedPipeClientProcessId(server, &pid); err != nil || pid != uint32(os.Getpid()) {
		t.Fatal(pid, err)
	}
	done := make(chan error, 1)
	go func() { done <- writeFrame(c, helperReply{Ready: true}) }()
	var reply helperReply
	if err = readFrame(s, &reply); err != nil || !reply.Ready {
		t.Fatal(reply, err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
