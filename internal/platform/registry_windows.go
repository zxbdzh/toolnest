//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"runtime"
	"syscall"
	"toolnest/internal/core"
	"unsafe"
)

const systemEnvironment = `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`
const testPrefix = `Software\ToolNest\Tests\`

type windowsStore struct {
	sid      string
	testRoot string
}

// NewRegistry 默认读 HKU/<原始SID>/Environment 与 HKLM 系统 Environment。
// HKU/SID 与调用者 HKCU 对应，提权到另一管理员账户时也绝不改变用户目标。
func NewRegistry(options Options) (*Registry, error) {
	if err := options.validate(); err != nil {
		return nil, err
	}
	sid, err := processSID(windows.CurrentProcess())
	if err != nil {
		return nil, err
	}
	r := &Registry{store: &windowsStore{sid: sid, testRoot: options.TestRoot}, sid: sid, options: options}
	r.start = startElevatedHelper
	return r, nil
}
func processSID(process windows.Handle) (string, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}
func (s *windowsStore) location(scope core.Scope) (registry.Key, string, error) {
	if scope != core.User && scope != core.System {
		return 0, "", ErrUnauthorized
	}
	if s.testRoot != "" {
		return registry.USERS, s.sid + `\` + testPrefix + s.testRoot + `\` + string(scope), nil
	}
	if scope == core.User {
		return registry.USERS, s.sid + `\Environment`, nil
	}
	return registry.LOCAL_MACHINE, systemEnvironment, nil
}

func (s *windowsStore) preflight(ctx context.Context, scopes []core.Scope) error {
	for _, scope := range scopes {
		if err := ctx.Err(); err != nil {
			return err
		}
		root, path, err := s.location(scope)
		if err != nil {
			return err
		}
		key, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.SET_VALUE|registry.WOW64_64KEY)
		if err != nil {
			return fmt.Errorf("所选 PATH 范围不可写，整次恢复尚未开始: %w", err)
		}
		key.Close()
	}
	return nil
}
func (s *windowsStore) read(ctx context.Context) (core.State, error) {
	var state core.State
	var err error
	state.User, err = s.readValue(ctx, core.User)
	if err != nil {
		return state, fmt.Errorf("读取用户 PATH: %w", err)
	}
	state.System, err = s.readValue(ctx, core.System)
	if err != nil {
		return state, fmt.Errorf("读取系统 PATH: %w", err)
	}
	return state, nil
}
func (s *windowsStore) readValue(ctx context.Context, scope core.Scope) (core.Value, error) {
	if err := ctx.Err(); err != nil {
		return core.Value{}, err
	}
	root, path, err := s.location(scope)
	if err != nil {
		return core.Value{}, err
	}
	// OpenKey 只有 QUERY_VALUE，读取缺键/缺值不会创建或修复任何内容。
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return core.Value{}, nil
	}
	if err != nil {
		return core.Value{}, err
	}
	defer key.Close()
	for tries := 0; tries < 4; tries++ {
		n, _, err := key.GetValue("Path", nil)
		if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
			return core.Value{}, nil
		}
		if err != nil && !errors.Is(err, syscall.ERROR_MORE_DATA) {
			return core.Value{}, err
		}
		if n > core.MaxPathBytes {
			return core.Value{}, errors.New("PATH 超出原始字节上限")
		}
		data := make([]byte, n)
		count, typ, err := key.GetValue("Path", data)
		if errors.Is(err, syscall.ERROR_MORE_DATA) {
			continue
		}
		if err != nil {
			return core.Value{}, err
		}
		if count > len(data) {
			continue
		}
		value := core.Value{Exists: true, Type: typ, Bytes: data[:count]}
		return value, value.Validate()
	}
	return core.Value{}, core.ErrChanged
}

var ntSetValueKey = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtSetValueKey")

type nativeUnicodeString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        *uint16
}

func (s *windowsStore) write(ctx context.Context, scope core.Scope, value core.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	root, path, err := s.location(scope)
	if err != nil {
		return err
	}
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.SET_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) && !value.Exists {
		return nil
	}
	if err != nil {
		return err
	}
	defer key.Close()
	if !value.Exists {
		err = key.DeleteValue("Path")
		if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
			return nil
		}
		return err
	}
	name, _ := windows.UTF16PtrFromString("Path")
	var data *byte
	if len(value.Bytes) > 0 {
		data = &value.Bytes[0]
	}
	// Win32 字符串写入可能补终止符。快照恢复必须保留异常 UTF-16、缺终止符和零字节值，
	// 所以固定 Path 名称并用原生 DataSize；绝不暴露任意键/值能力给助手。
	nativeName := nativeUnicodeString{Length: 8, MaximumLength: 10, Buffer: name}
	status, _, _ := ntSetValueKey.Call(uintptr(key), uintptr(unsafe.Pointer(&nativeName)), 0, uintptr(value.Type), uintptr(unsafe.Pointer(data)), uintptr(len(value.Bytes)))
	runtime.KeepAlive(value.Bytes)
	runtime.KeepAlive(name)
	if int32(status) < 0 {
		return windows.NTStatus(status).Errno()
	}
	return nil
}
func (s *windowsStore) notify(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.testRoot != "" {
		return nil
	} // 隔离验收不向桌面广播虚假的环境变更。
	text, _ := windows.UTF16PtrFromString("Environment")
	var result uintptr
	ok, _, err := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW").Call(0xffff, 0x001a, 0, uintptr(unsafe.Pointer(text)), 0x0002, 5000, uintptr(unsafe.Pointer(&result)))
	if ok == 0 {
		if err != syscall.Errno(0) {
			return err
		}
		return errors.New("环境变更通知超时")
	}
	return nil
}
