package update

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var moveFileEx = kernel32.NewProc("MoveFileExW")

func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func replaceFile(from, to string) error {
	source, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	target, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	// 同卷原子替换元数据并要求刷盘；避免删旧状态后再重命名的断电窗口。
	ok, _, callErr := moveFileEx.Call(uintptr(unsafe.Pointer(source)), uintptr(unsafe.Pointer(target)), 0x1|0x8)
	if ok == 0 {
		return callErr
	}
	return nil
}

func waitParent(ctx context.Context, pid int) error {
	if pid <= 0 {
		return ErrArchive
	}
	handle, err := syscall.OpenProcess(0x00100000, false, uint32(pid))
	if err != nil {
		if err == syscall.Errno(87) {
			return nil
		}
		return err
	}
	defer syscall.CloseHandle(handle)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		event, err := syscall.WaitForSingleObject(handle, 100)
		if err != nil {
			return err
		}
		if event == syscall.WAIT_OBJECT_0 {
			return nil
		}
	}
}

func lockState(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	var overlapped windows.Overlapped
	// 内核随进程退出释放锁；不能用永久 O_EXCL 标记把崩溃恢复入口锁死。
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
		file.Close()
		return nil, ErrBusy
	}
	return func() { _ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped); _ = file.Close() }, nil
}
