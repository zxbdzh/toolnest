//go:build windows

package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Instance owns the primary lock until main has actually quit, not until its window hides.
type Instance struct {
	mutex windows.Handle
	pipe  windows.Handle
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once
}

// AcquireInstance must run before creating schedulers or UI. secondary == true means return from main.
// onLaunch runs on a worker; bind Controller.SecondLaunch, whose Post dispatches to UI.
// namespace must be a stable application ID (e.g. ToolNest); tests use unique IDs.
func AcquireInstance(ctx context.Context, namespace string, launch Launch, onLaunch func(Launch)) (*Instance, bool, error) {
	if namespace == "" || len(namespace) > 128 || onLaunch == nil {
		return nil, false, errors.New("stable instance namespace and callback required")
	}
	account, err := CurrentAccount()
	if err != nil {
		return nil, false, err
	}
	hash := sha256.Sum256([]byte(namespace + "\n" + account.SID))
	id := hex.EncodeToString(hash[:16])
	// 显式 SID DACL 防跨用户通知；全局 mutex 防同一用户跨会话重复运行。
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + account.SID + ")")
	if err != nil {
		return nil, false, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	mutexName, _ := windows.UTF16PtrFromString(`Global\ToolNest.` + id)
	mutex, err := windows.CreateMutex(&sa, false, mutexName)
	runtime.KeepAlive(sd)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if mutex != 0 {
			windows.CloseHandle(mutex)
		}
		return nil, false, fmt.Errorf("single-instance mutex: %w", err)
	}
	pipeName := `\\.\pipe\ToolNest.` + id
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		defer windows.CloseHandle(mutex)
		// 二次自启无需唤回，不连接管道也不会占用 UI 或重新建立调度器。
		if launch == AutostartLaunch {
			return nil, true, nil
		}
		if err = notifyPrimary(ctx, pipeName); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}
	name, _ := windows.UTF16PtrFromString(pipeName)
	// 非阻塞消息管道保证取消和退出有界；仅接受本机客户端。
	pipe, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE, windows.PIPE_TYPE_MESSAGE|windows.PIPE_READMODE_MESSAGE|windows.PIPE_NOWAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 16, 16, 0, &sa)
	runtime.KeepAlive(sd)
	if err != nil {
		windows.CloseHandle(mutex)
		return nil, false, fmt.Errorf("single-instance pipe: %w", err)
	}
	i := &Instance{mutex: mutex, pipe: pipe, stop: make(chan struct{}), done: make(chan struct{})}
	go i.serve(onLaunch)
	return i, false, nil
}

var allowForegroundProc = windows.NewLazySystemDLL("user32.dll").NewProc("AllowSetForegroundWindow")

func notifyPrimary(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	p, _ := windows.UTF16PtrFromString(name)
	for {
		h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			// 只把本次前台权限传给同 SID 的服务器 PID，而不是 ASFW_ANY。
			var pid uint32
			if e := windows.GetNamedPipeServerProcessId(h, &pid); e != nil {
				windows.CloseHandle(h)
				return e
			}
			if e := verifyProcessUser(pid); e != nil {
				windows.CloseHandle(h)
				return e
			}
			_, _, _ = allowForegroundProc.Call(uintptr(pid))
			defer windows.CloseHandle(h)
			state := uint32(windows.PIPE_READMODE_MESSAGE | windows.PIPE_NOWAIT)
			if err = windows.SetNamedPipeHandleState(h, &state, nil, nil); err != nil {
				return err
			}
			var n uint32
			if err = windows.WriteFile(h, []byte{1}, &n, nil); err != nil {
				return err
			}
			if n != 1 {
				return errors.New("primary notification incomplete")
			}
			// 主进程验证客户端 SID 之后再确认；客户端不能提前退出使身份验证失效。
			for {
				var ack [1]byte
				err = windows.ReadFile(h, ack[:], &n, nil)
				if err == nil && n == 1 && ack[0] == 2 {
					return nil
				}
				if err != nil && !errors.Is(err, windows.ERROR_NO_DATA) {
					return err
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(10 * time.Millisecond):
				}
			}
		}
		if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return fmt.Errorf("notify primary: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("primary instance did not become ready: %w", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func verifyProcessUser(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	var token windows.Token
	if err = windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	defer token.Close()
	u, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	a, err := CurrentAccount()
	if err != nil {
		return err
	}
	if u.User.Sid.String() != a.SID {
		return ErrNotOwned
	}
	return nil
}
func (i *Instance) serve(fn func(Launch)) {
	defer close(i.done)
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	connected := false
	var since time.Time
	for {
		select {
		case <-i.stop:
			return
		case <-ticker.C:
		}
		if !connected {
			err := windows.ConnectNamedPipe(i.pipe, nil)
			if err == nil || errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
				connected = true
				since = time.Now()
			} else if errors.Is(err, windows.ERROR_NO_DATA) {
				windows.DisconnectNamedPipe(i.pipe)
			}
			if !connected {
				continue
			}
		}
		var data [8]byte
		var n uint32
		err := windows.ReadFile(i.pipe, data[:], &n, nil)
		if err == nil && n == 1 && data[0] == 1 {
			var pid uint32
			if windows.GetNamedPipeClientProcessId(i.pipe, &pid) == nil && verifyProcessUser(pid) == nil {
				fn(ManualLaunch)
				var written uint32
				if windows.WriteFile(i.pipe, []byte{2}, &written, nil) == nil && written == 1 {
					n = 0
				} // 等客户端读完关闭，再断开。
			}
		}
		if n > 0 || (err != nil && !errors.Is(err, windows.ERROR_NO_DATA)) || time.Since(since) > time.Second {
			windows.DisconnectNamedPipe(i.pipe)
			connected = false
		}
	}
}
func (i *Instance) Close() {
	if i == nil {
		return
	}
	i.once.Do(func() { close(i.stop); <-i.done; windows.CloseHandle(i.pipe); windows.CloseHandle(i.mutex) })
}

// ProcessExecutable is a convenience for binding RunManager; it never guesses old executable paths.
func ProcessExecutable() (string, error) { return os.Executable() }
