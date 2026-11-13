//go:build windows

package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const currentRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

type WindowsRunStore struct {
	mu      sync.Mutex
	path    string
	account Account
}

// CurrentAccount obtains the token SID; no user-supplied SID is trusted for HKCU writes.
func CurrentAccount() (Account, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return Account{}, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return Account{}, err
	}
	a := Account{SID: user.User.Sid.String()}
	name, domain, _, err := user.User.Sid.LookupAccount("")
	if err == nil {
		a.Name = name
		if domain != "" {
			a.Name = domain + `\` + name
		}
	}
	return a, nil
}

// RequireUnelevated is a startup guard, not an elevation launcher. Call before constructing UI.
func RequireUnelevated() error {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer token.Close()
	if token.IsElevated() {
		return errors.New("ToolNest 主 UI 必须以普通权限启动；请从普通桌面重新打开")
	}
	return nil
}

func NewWindowsRunStore() (*WindowsRunStore, error) {
	a, err := CurrentAccount()
	if err != nil {
		return nil, err
	}
	return &WindowsRunStore{path: currentRunKey, account: a}, nil
}

// NewIsolatedWindowsRunStore never accepts a real Run/environment key; tests use a unique subtree.
func NewIsolatedWindowsRunStore(subkey string) (*WindowsRunStore, error) {
	const prefix = `Software\ToolNest\Tests\`
	if !strings.HasPrefix(subkey, prefix) || len(subkey) == len(prefix) || strings.Contains(subkey, "/") {
		return nil, errors.New("isolated store must be under HKCU Software/ToolNest/Tests")
	}
	for _, p := range strings.Split(strings.TrimPrefix(subkey, prefix), `\`) {
		if p == "" || p == "." || p == ".." {
			return nil, errors.New("invalid isolated subkey")
		}
	}
	a, e := CurrentAccount()
	if e != nil {
		return nil, e
	}
	return &WindowsRunStore{path: subkey, account: a}, nil
}
func (s *WindowsRunStore) check(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if name != ToolNestRunName && name != LegacyRunName {
		return ErrNotOwned
	}
	a, e := CurrentAccount()
	if e != nil {
		return e
	}
	if a.SID != s.account.SID {
		return ErrNotOwned
	}
	return nil
}
func (s *WindowsRunStore) read(name string) (RunValue, error) {
	k, e := registry.OpenKey(registry.CURRENT_USER, s.path, registry.QUERY_VALUE)
	if errors.Is(e, registry.ErrNotExist) {
		return RunValue{}, nil
	}
	if e != nil {
		return RunValue{}, e
	}
	defer k.Close()
	for i := 0; i < 4; i++ {
		n, typ, e := k.GetValue(name, nil)
		if errors.Is(e, registry.ErrNotExist) {
			return RunValue{}, nil
		}
		if e != nil && e != registry.ErrShortBuffer {
			return RunValue{}, e
		}
		if n > 65536 {
			return RunValue{}, errors.New("Run value exceeds safe size")
		}
		data := make([]byte, n)
		n, typ, e = k.GetValue(name, data)
		if e == registry.ErrShortBuffer {
			continue
		}
		if errors.Is(e, registry.ErrNotExist) {
			return RunValue{}, nil
		}
		if e != nil {
			return RunValue{}, e
		}
		return RunValue{true, typ, data[:n]}, nil
	}
	return RunValue{}, ErrConflict
}
func (s *WindowsRunStore) ReadRun(ctx context.Context, name string) (RunValue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(ctx, name); e != nil {
		return RunValue{}, e
	}
	return s.read(name)
}
func (s *WindowsRunStore) ReplaceRun(ctx context.Context, name string, before, after RunValue) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(ctx, name); e != nil {
		return e
	}
	now, e := s.read(name)
	if e != nil {
		return e
	}
	if !now.Equal(before) {
		return ErrConflict
	}
	// Windows 注册表没有单值 CAS；在持锁下紧邻写入重读并读回，不承诺抵抗外部同一瞬间写入。
	if after.Exists && after.Type != RunString {
		return ErrNotOwned
	}
	if after.Exists {
		if _, e := decodeRun(after); e != nil {
			return e
		}
	}
	var k registry.Key
	if after.Exists {
		k, _, e = registry.CreateKey(registry.CURRENT_USER, s.path, registry.SET_VALUE|registry.QUERY_VALUE)
	} else {
		k, e = registry.OpenKey(registry.CURRENT_USER, s.path, registry.SET_VALUE|registry.QUERY_VALUE)
	}
	if e != nil {
		return e
	}
	defer k.Close()
	now, e = s.read(name)
	if e != nil {
		return e
	}
	if !now.Equal(before) {
		return ErrConflict
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if after.Exists {
		var text string
		text, e = decodeRun(after)
		if e == nil {
			e = k.SetStringValue(name, text)
		}
	} else {
		e = k.DeleteValue(name)
	}
	if e != nil {
		return e
	}
	check, e := s.read(name)
	if e != nil {
		return e
	}
	if !check.Equal(after) {
		return fmt.Errorf("Run readback mismatch: %w", ErrConflict)
	}
	return nil
}
