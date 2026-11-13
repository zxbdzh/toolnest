//go:build windows

package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\ToolNest`
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func DefaultInstallDir() (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Programs", "ToolNest"), nil
}

func ValidateInstallDir(dir string) error {
	expected, err := DefaultInstallDir()
	if err != nil {
		return err
	}
	clean := filepath.Clean(dir)
	if !filepath.IsAbs(dir) || !strings.EqualFold(clean, expected) {
		return errors.New("only the fixed per-user ToolNest installation directory is allowed")
	}
	// 不信任可重定向的安装目录，避免精确文件删除经 junction 落到用户数据。
	for p := clean; ; p = filepath.Dir(p) {
		ptr, err := windows.UTF16PtrFromString(p)
		if err != nil {
			return err
		}
		attrs, err := windows.GetFileAttributes(ptr)
		if err == nil && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("reparse installation path refused: %s", p)
		}
		if err != nil && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return err
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}

// AcquireInstallation is held for the ENTIRE normal process lifetime, including
// tray mode, all critical writes and graceful shutdown. NSIS holds the same lock.
func AcquireInstallation(dir string) (func(), error) {
	if err := ValidateInstallDir(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return acquireLockFile(filepath.Join(dir, ".toolnest-maintenance.lock"))
}

// 锁原语独立出来供临时目录测试；公开入口仍只允许固定生产安装路径。
func acquireLockFile(path string) (func(), error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(ptr, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return nil, ErrBusy
		}
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("maintenance lock must be a nonredirected regular file with one link")
	}
	var once sync.Once
	return func() { once.Do(func() { _ = windows.CloseHandle(handle) }) }, nil
}

// CheckIdle conservatively blocks any ToolNest.exe, including a legacy build
// that has not bound AcquireInstallation. No process is terminated.
func CheckIdle(dir string) error {
	if err := ValidateInstallDir(dir); err != nil {
		return err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	err = windows.Process32First(snapshot, &entry)
	for err == nil {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "ToolNest.exe") {
			return ErrBusy
		}
		err = windows.Process32Next(snapshot, &entry)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	return nil
}

func ValidateExisting(dir string) error {
	if err := ValidateInstallDir(dir); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	occupied := false
	owned := map[string]bool{"ToolNest.exe": true, "ToolNest-maintenance.exe": true, "release-identity.json": true, "Uninstall.exe": true, ".toolnest-maintenance.lock": true}
	for _, entry := range entries {
		if entry.Name() != ".toolnest-maintenance.lock" {
			occupied = true
		}
		if owned[entry.Name()] {
			ptr, e := windows.UTF16PtrFromString(filepath.Join(dir, entry.Name()))
			if e != nil {
				return e
			}
			attrs, e := windows.GetFileAttributes(ptr)
			if e != nil {
				return e
			}
			if attrs&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
				return errors.New("owned installation filename is redirected or not a regular file")
			}
		}
	}
	if !occupied {
		return nil
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return errors.New("occupied installation directory has no verified ToolNest registration")
	}
	defer key.Close()
	marker, kind, e1 := key.GetStringValue("InstallerOwner")
	location, kind2, e2 := key.GetStringValue("InstallLocation")
	if e1 != nil || e2 != nil || kind != registry.SZ || kind2 != registry.SZ || marker != InstallMarker || !strings.EqualFold(filepath.Clean(location), filepath.Clean(dir)) {
		return errors.New("installation ownership mismatch")
	}
	return nil
}

// RemoveOwnedRun never touches PATH, RunOnce, scheduled tasks, or preferences.
func RemoveOwnedRun(dir string) (bool, error) {
	if err := ValidateInstallDir(dir); err != nil {
		return false, err
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer key.Close()
	command, kind, err := key.GetStringValue(RunName)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !OwnedRun(RunValue{RunName, kind, command}, filepath.Join(dir, "ToolNest.exe")) {
		return false, nil
	}
	// 注册表没有 compare-and-delete；删除前重读，任何类型或命令变化都保留。
	again, againType, err := key.GetStringValue(RunName)
	if err != nil {
		return false, err
	}
	if again != command || againType != kind {
		return false, errors.New("Run value changed during removal")
	}
	return true, key.DeleteValue(RunName)
}

// SyncInstalledVersion runs only after startup confirmation, and never
// re-registers autostart from a retained preference.
func SyncInstalledVersion(dir, version string) error {
	if err := ValidateExisting(dir); err != nil {
		return err
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue("DisplayVersion", version)
}
