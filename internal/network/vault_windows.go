package network

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

type WindowsVault struct{ Namespace string }

type credential struct {
	Flags, Type             uint32
	TargetName, Comment     *uint16
	LastWritten             syscall.Filetime
	BlobSize                uint32
	Blob                    *byte
	Persist, AttributeCount uint32
	Attributes              uintptr
	Alias, Username         *uint16
}

var credentialDLL = windows.NewLazySystemDLL("advapi32.dll")
var credRead = credentialDLL.NewProc("CredReadW")
var credWrite = credentialDLL.NewProc("CredWriteW")
var credDelete = credentialDLL.NewProc("CredDeleteW")
var credFree = credentialDLL.NewProc("CredFree")

func (vault WindowsVault) target(key string) (*uint16, error) {
	if vault.Namespace == "" || key == "" || len(vault.Namespace)+len(key) > 200 || strings.ContainsAny(vault.Namespace+key, "\x00\r\n") {
		return nil, ErrCredentials
	}
	return syscall.UTF16PtrFromString(vault.Namespace + "/proxy/" + key)
}

func (vault WindowsVault) Read(ctx context.Context, key string) (Credentials, error) {
	if err := ctx.Err(); err != nil {
		return Credentials{}, err
	}
	target, err := vault.target(key)
	if err != nil {
		return Credentials{}, err
	}
	var stored *credential
	ok, _, _ := credRead.Call(uintptr(unsafe.Pointer(target)), 1, 0, uintptr(unsafe.Pointer(&stored)))
	if ok == 0 {
		return Credentials{}, ErrCredentials
	}
	defer credFree.Call(uintptr(unsafe.Pointer(stored)))
	if stored.BlobSize > 2560 || stored.BlobSize%2 != 0 || stored.BlobSize > 0 && stored.Blob == nil {
		return Credentials{}, ErrCredentials
	}
	var username []uint16
	if stored.Username != nil {
		for offset := 0; offset < 513; offset++ {
			value := *(*uint16)(unsafe.Add(unsafe.Pointer(stored.Username), offset*2))
			if value == 0 {
				break
			}
			username = append(username, value)
		}
	}
	password := unsafe.Slice((*uint16)(unsafe.Pointer(stored.Blob)), int(stored.BlobSize)/2)
	return Credentials{Username: string(utf16.Decode(username)), Password: string(utf16.Decode(password))}, nil
}

func (vault WindowsVault) Write(ctx context.Context, key string, secret Credentials) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := vault.target(key)
	if err != nil {
		return err
	}
	username, err := syscall.UTF16PtrFromString(secret.Username)
	if err != nil || strings.ContainsRune(secret.Password, 0) {
		return ErrCredentials
	}
	password := utf16.Encode([]rune(secret.Password))
	if len(password) > 1280 {
		return ErrCredentials
	}
	// Credential Manager 负责当前用户加密；不把秘密写入应用 JSON 或日志。
	stored := credential{Type: 1, TargetName: target, Username: username, Persist: 2, BlobSize: uint32(len(password) * 2)}
	if len(password) > 0 {
		stored.Blob = (*byte)(unsafe.Pointer(&password[0]))
	}
	ok, _, _ := credWrite.Call(uintptr(unsafe.Pointer(&stored)), 0)
	for index := range password {
		password[index] = 0
	}
	runtime.KeepAlive(username)
	runtime.KeepAlive(target)
	if ok == 0 {
		return ErrCredentials
	}
	return nil
}

func (vault WindowsVault) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := vault.target(key)
	if err != nil {
		return err
	}
	ok, _, callErr := credDelete.Call(uintptr(unsafe.Pointer(target)), 1, 0)
	if ok == 0 && !errors.Is(callErr, syscall.Errno(1168)) {
		return ErrCredentials
	}
	return nil
}
