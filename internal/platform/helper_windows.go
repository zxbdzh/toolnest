//go:build windows

package platform

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"toolnest/internal/core"
	"unsafe"
)

const helperFlag = "--toolnest-path-helper"
const pipePrefix = `\\.\pipe\ToolNest.PathRestore.`
const helperLifetime = 5 * time.Minute

// 不把提权助手的 impersonation token 暴露给普通权限父进程。
const helperPipeClientFlags = windows.FILE_FLAG_OVERLAPPED | windows.SECURITY_SQOS_PRESENT | windows.SECURITY_IDENTIFICATION

type pipeStream struct {
	handle windows.Handle
	ctx    context.Context
	once   sync.Once
}

func (p *pipeStream) close() {
	p.once.Do(func() { _ = windows.CancelIoEx(p.handle, nil); _ = windows.CloseHandle(p.handle) })
}
func (p *pipeStream) Read(b []byte) (int, error)  { return p.transfer(b, false) }
func (p *pipeStream) Write(b []byte) (int, error) { return p.transfer(b, true) }
func (p *pipeStream) transfer(b []byte, write bool) (int, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(event)
	ov := windows.Overlapped{HEvent: event}
	var n uint32
	if write {
		err = windows.WriteFile(p.handle, b, &n, &ov)
	} else {
		err = windows.ReadFile(p.handle, b, &n, &ov)
	}
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		err = waitOverlapped(p.ctx, p.handle, &ov, &n)
	}
	return int(n), err
}
func waitOverlapped(ctx context.Context, h windows.Handle, ov *windows.Overlapped, n *uint32) error {
	for {
		if err := ctx.Err(); err != nil {
			_ = windows.CancelIoEx(h, ov)
			_ = windows.GetOverlappedResult(h, ov, n, true)
			return err
		}
		event, err := windows.WaitForSingleObject(ov.HEvent, 100)
		if err != nil {
			// 无论等待为何失败，都先排空内核仍可能持有的 OVERLAPPED 指针。
			_ = windows.CancelIoEx(h, ov)
			_ = windows.GetOverlappedResult(h, ov, n, true)
			return err
		}
		if event == windows.WAIT_OBJECT_0 {
			return windows.GetOverlappedResult(h, ov, n, false)
		}
		if event != uint32(windows.WAIT_TIMEOUT) {
			_ = windows.CancelIoEx(h, ov)
			_ = windows.GetOverlappedResult(h, ov, n, true)
			return errors.New("助手管道等待失败")
		}
	}
}

type elevatedSession struct {
	stream  *pipeStream
	process windows.Handle
	cancel  context.CancelFunc
}

func (s *elevatedSession) write(ctx context.Context, scope core.Scope, value core.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeFrame(s.stream, helperCommand{Operation: "write", Scope: scope, Value: &value}); err != nil {
		return err
	}
	var reply helperReply
	if err := readFrame(s.stream, &reply); err != nil {
		return err
	}
	return validateResponseError(reply.Error)
}
func (s *elevatedSession) close() {
	// 关闭通道即撤销能力，绝不终止主程序或其他进程。
	s.cancel()
	s.stream.close()
	_ = windows.CloseHandle(s.process)
}

func startElevatedHelper(ctx context.Context, a authorization) (helperSession, error) {
	if err := a.validate(); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	pipeName := pipePrefix + hex.EncodeToString(nonce[:])
	pipe16, _ := windows.UTF16PtrFromString(pipeName)
	// 只允许原始用户、管理员和 SYSTEM；即使管理员凭据属于别人，也只能连接本次管道。
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + a.OriginalSID + ")S:(ML;;NW;;;ME)")
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	handle, err := windows.CreateNamedPipe(pipe16, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 65536, 65536, 0, &sa)
	runtime.KeepAlive(sd)
	if err != nil {
		return nil, err
	}
	// 授权期间响应取消；授权成功后 core 仍可能用 WithoutCancel 回滚，不能提前杀死会话。
	lifetime, cancel := context.WithTimeout(context.WithoutCancel(ctx), helperLifetime)
	stopAuthorizationCancel := context.AfterFunc(ctx, cancel)
	defer stopAuthorizationCancel()
	stream := &pipeStream{handle: handle, ctx: lifetime}
	cleanup := func() { cancel(); stream.close() }
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		cleanup()
		return nil, err
	}
	defer windows.CloseHandle(event)
	ov := windows.Overlapped{HEvent: event}
	connectErr := windows.ConnectNamedPipe(handle, &ov)
	if connectErr != nil && !errors.Is(connectErr, windows.ERROR_IO_PENDING) && !errors.Is(connectErr, windows.ERROR_PIPE_CONNECTED) {
		cleanup()
		return nil, connectErr
	}
	args := []string{helperFlag, "--pipe", pipeName, "--caller", strconv.Itoa(os.Getpid()), "--digest", a.digest()}
	process, err := shellElevate(executable, args)
	if err != nil {
		// UAC 取消时连接仍可能 pending；必须排空后才能释放 OVERLAPPED/event。
		if errors.Is(connectErr, windows.ERROR_IO_PENDING) {
			_ = windows.CancelIoEx(handle, &ov)
			var n uint32
			_ = windows.GetOverlappedResult(handle, &ov, &n, true)
		}
		cleanup()
		return nil, err
	}
	session := &elevatedSession{stream: stream, process: process, cancel: cancel}
	if errors.Is(connectErr, windows.ERROR_IO_PENDING) {
		var n uint32
		if err = waitOverlapped(lifetime, handle, &ov, &n); err != nil {
			session.close()
			return nil, err
		}
	}
	var clientPID uint32
	if err = windows.GetNamedPipeClientProcessId(handle, &clientPID); err != nil {
		session.close()
		return nil, err
	}
	expectedPID, err := windows.GetProcessId(process)
	if err != nil || clientPID != expectedPID {
		session.close()
		return nil, ErrUnauthorized
	}
	if err = verifySameImage(process, executable); err != nil {
		session.close()
		return nil, err
	}
	var token windows.Token
	if err = windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		session.close()
		return nil, err
	}
	elevated := token.IsElevated()
	token.Close()
	if !elevated {
		session.close()
		return nil, ErrUnauthorized
	}
	if err = writeFrame(stream, a); err != nil {
		session.close()
		return nil, err
	}
	var reply helperReply
	if err = readFrame(stream, &reply); err != nil {
		session.close()
		return nil, err
	}
	if !reply.Ready || reply.Error != "" {
		session.close()
		if reply.Error == ErrCancelled.Error() {
			return nil, ErrCancelled
		}
		if reply.Error == core.ErrChanged.Error() {
			return nil, core.ErrChanged
		}
		return nil, errors.Join(ErrUnauthorized, validateResponseError(reply.Error))
	}
	return session, nil
}

type shellExecuteInfo struct {
	Size       uint32
	Mask       uint32
	Window     uintptr
	Verb       *uint16
	File       *uint16
	Parameters *uint16
	Directory  *uint16
	Show       int32
	Instance   uintptr
	IDList     uintptr
	Class      *uint16
	ClassKey   uintptr
	HotKey     uint32
	Icon       uintptr
	Process    windows.Handle
}

func shellElevate(executable string, args []string) (windows.Handle, error) {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(executable)
	escaped := make([]string, len(args))
	for i, a := range args {
		escaped[i] = syscall.EscapeArg(a)
	}
	params, _ := windows.UTF16PtrFromString(strings.Join(escaped, " "))
	info := shellExecuteInfo{Mask: 0x00000040 | 0x00000100, Verb: verb, File: file, Parameters: params, Show: 0}
	info.Size = uint32(unsafe.Sizeof(info))
	ok, _, err := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW").Call(uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(params)
	runtime.KeepAlive(file)
	runtime.KeepAlive(verb)
	if ok == 0 {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return 0, ErrCancelled
		}
		return 0, fmt.Errorf("请求系统 PATH 权限: %w", err)
	}
	if info.Process == 0 {
		return 0, ErrUnauthorized
	}
	return info.Process, nil
}

func verifySameImage(process windows.Handle, expected string) error {
	buffer := make([]uint16, 32768)
	n := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &n); err != nil {
		return err
	}
	actual := windows.UTF16ToString(buffer[:n])
	a, err := filepath.EvalSymlinks(actual)
	if err != nil {
		return err
	}
	b, err := filepath.EvalSymlinks(expected)
	if err != nil {
		return err
	}
	if !strings.EqualFold(a, b) {
		return errors.New("助手与调用者程序路径不一致")
	}
	ah, err := hashImage(actual)
	if err != nil {
		return err
	}
	bh, err := hashImage(expected)
	if err != nil {
		return err
	}
	if ah != bh {
		return ErrUnauthorized
	}
	return nil
}
func hashImage(path string) ([32]byte, error) {
	var out [32]byte
	file, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer file.Close()
	h := sha256.New()
	if _, err = io.Copy(h, file); err != nil {
		return out, err
	}
	copy(out[:], h.Sum(nil))
	return out, nil
}

// HandleHelper 必须在 main 的单实例、UI、设置、计划初始化之前调用。
// args 应为 os.Args[1:]；handled=true 时立即以 code 退出，绝不进入普通 UI。
func HandleHelper(ctx context.Context, args []string) (handled bool, code int, err error) {
	if len(args) == 0 || args[0] != helperFlag {
		return false, 0, nil
	}
	if len(args) != 7 || args[1] != "--pipe" || args[3] != "--caller" || args[5] != "--digest" {
		return true, 2, ErrUnauthorized
	}
	pipeName := args[2]
	suffix := strings.TrimPrefix(pipeName, pipePrefix)
	if !strings.HasPrefix(pipeName, pipePrefix) || len(suffix) != 64 {
		return true, 2, ErrUnauthorized
	}
	if _, e := hex.DecodeString(suffix); e != nil {
		return true, 2, ErrUnauthorized
	}
	pid64, e := strconv.ParseUint(args[4], 10, 32)
	if e != nil || pid64 == 0 {
		return true, 2, ErrUnauthorized
	}
	if len(args[6]) != 64 {
		return true, 2, ErrUnauthorized
	}
	if _, e = hex.DecodeString(args[6]); e != nil {
		return true, 2, ErrUnauthorized
	}
	var self windows.Token
	if e = windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &self); e != nil {
		return true, 3, e
	}
	elevated := self.IsElevated()
	self.Close()
	if !elevated {
		return true, 3, ErrUnauthorized
	}
	parent, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid64))
	if e != nil {
		return true, 3, e
	}
	defer windows.CloseHandle(parent)
	executable, e := os.Executable()
	if e != nil {
		return true, 3, e
	}
	if e = verifySameImage(parent, executable); e != nil {
		return true, 3, e
	}
	sid, e := processSID(parent)
	if e != nil {
		return true, 3, e
	}
	state, e := windows.WaitForSingleObject(parent, 0)
	if e != nil || state != uint32(windows.WAIT_TIMEOUT) {
		return true, 3, ErrUnauthorized
	}
	name, _ := windows.UTF16PtrFromString(pipeName)
	// 助手是高权限管道客户端，只允许身份识别，禁止父进程模拟助手取得管理员能力。
	handle, e := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, helperPipeClientFlags, 0)
	if e != nil {
		return true, 3, e
	}
	lifetime, cancel := context.WithTimeout(ctx, helperLifetime)
	watchDone := make(chan struct{})
	go func() { defer close(watchDone); watchCaller(lifetime, cancel, parent) }()
	// 先停止观察再关闭 parent，避免句柄复用后观察到不相关进程。
	defer func() { cancel(); <-watchDone }()
	stream := &pipeStream{handle: handle, ctx: lifetime}
	defer stream.close()
	var serverPID uint32
	if e = windows.GetNamedPipeServerProcessId(handle, &serverPID); e != nil || serverPID != uint32(pid64) {
		return true, 3, ErrUnauthorized
	}
	var a authorization
	if e = readFrame(stream, &a); e != nil {
		return true, 3, e
	}
	if e = a.validate(); e != nil {
		return true, 3, e
	}
	if a.OriginalSID != sid || a.digest() != args[6] || a.TestRoot != "" || !containsScope(a.Scopes, core.System) {
		return true, 3, ErrUnauthorized
	}
	store := &windowsStore{sid: sid}
	current, e := store.read(lifetime)
	if e != nil {
		return true, 3, e
	}
	if !current.Equal(a.Before) {
		_ = writeFrame(stream, helperReply{Error: core.ErrChanged.Error()})
		return true, 4, core.ErrChanged
	}
	// UAC 只确认程序身份；安全桌面之后再次展示确定范围、原始账户及内容指纹，防止请求被替换。
	if !confirmHelper(a) {
		_ = writeFrame(stream, helperReply{Error: ErrCancelled.Error()})
		return true, 5, ErrCancelled
	}
	// 确认框期间原始进程可能退出；不能把旧 PID/请求继续当成有效授权。
	if e = callerAlive(parent); e != nil {
		return true, 3, e
	}
	if e = lifetime.Err(); e != nil {
		return true, 3, e
	}
	if e = store.preflight(lifetime, []core.Scope{core.System}); e != nil {
		_ = writeFrame(stream, helperReply{Error: e.Error()})
		return true, 3, e
	}
	if e = writeFrame(stream, helperReply{Ready: true}); e != nil {
		return true, 3, e
	}
	e = serveWrites(lifetime, stream, a, &callerBoundStore{rawStore: store, parent: parent})
	if errors.Is(e, io.EOF) || errors.Is(e, windows.ERROR_BROKEN_PIPE) {
		return true, 0, nil
	}
	if e != nil {
		return true, 6, e
	}
	return true, 0, nil
}
func callerAlive(parent windows.Handle) error {
	state, err := windows.WaitForSingleObject(parent, 0)
	if err != nil {
		return err
	}
	if state != uint32(windows.WAIT_TIMEOUT) {
		return ErrUnauthorized
	}
	return nil
}
func watchCaller(ctx context.Context, cancel context.CancelFunc, parent windows.Handle) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if callerAlive(parent) != nil {
				cancel()
				return
			}
		}
	}
}

type callerBoundStore struct {
	rawStore
	parent windows.Handle
}

func (s *callerBoundStore) read(ctx context.Context) (core.State, error) {
	if err := callerAlive(s.parent); err != nil {
		return core.State{}, err
	}
	return s.rawStore.read(ctx)
}
func (s *callerBoundStore) write(ctx context.Context, scope core.Scope, value core.Value) error {
	if err := callerAlive(s.parent); err != nil {
		return err
	}
	return s.rawStore.write(ctx, scope, value)
}

func confirmHelper(a authorization) bool {
	scopes := "系统 PATH"
	if containsScope(a.Scopes, core.User) {
		scopes = "用户 PATH + 系统 PATH"
	}
	message := fmt.Sprintf("确认 ToolNest 已预览的恢复？\n\n范围：%s\n原始用户 SID：%s\n确认令牌：%s\n请求 SHA-256：%s\n\n系统 PATH：类型 %d，%d 字节 → 类型 %d，%d 字节\n\n此助手只允许本次系统 PATH 写入与必要回滚，不运行其他命令。", scopes, a.OriginalSID, a.Token, a.digest(), a.Before.System.Type, len(a.Before.System.Bytes), a.Target.System.Type, len(a.Target.System.Bytes))
	text, _ := windows.UTF16PtrFromString(message)
	title, _ := windows.UTF16PtrFromString("ToolNest · 确认受限恢复")
	result, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x00000004|0x00000030|0x00000100|0x00010000)
	return result == 6
}
