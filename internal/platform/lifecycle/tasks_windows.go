//go:build windows

package lifecycle

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

//go:embed tasks.ps1
var taskScript string

type WindowsTaskStore struct {
	account     Account
	executables []string
	shell       string
}

// NewWindowsTaskStore accepts only known old executable paths, never a script/command/program.
func NewWindowsTaskStore(knownExecutables []string) (*WindowsTaskStore, error) {
	if len(knownExecutables) == 0 {
		return nil, errors.New("explicit old executables required")
	}
	for _, e := range knownExecutables {
		if err := validateExe(e); err != nil {
			return nil, err
		}
	}
	a, e := CurrentAccount()
	if e != nil {
		return nil, e
	}
	system, e := windows.GetSystemDirectory()
	if e != nil {
		return nil, e
	}
	return &WindowsTaskStore{a, append([]string(nil), knownExecutables...), filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe")}, nil
}
func encodedScript(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[2*i:], v)
	}
	return base64.StdEncoding.EncodeToString(b)
}

type taskRequest struct {
	Operation       string   `json:"operation"`
	SID             string   `json:"sid"`
	Name            string   `json:"name"`
	Executables     []string `json:"executables"`
	ExpectedXML     string   `json:"expectedXML"`
	ExpectedEnabled bool     `json:"expectedEnabled"`
	Enabled         bool     `json:"enabled"`
}

type limitedOutput struct {
	bytes.Buffer
	limit int
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("task output exceeds limit")
	}
	return b.Buffer.Write(p)
}
func (s *WindowsTaskStore) call(ctx context.Context, r taskRequest) (TaskValue, error) {
	if r.Name != "PathGuard-PATH-Backup-"+s.account.SID {
		return TaskValue{}, ErrNotOwned
	}
	a, err := CurrentAccount()
	if err != nil {
		return TaskValue{}, err
	}
	if a.SID != s.account.SID {
		return TaskValue{}, ErrNotOwned
	}
	r.SID = a.SID
	r.Executables = append([]string(nil), s.executables...)
	data, err := json.Marshal(r)
	if err != nil {
		return TaskValue{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	// 固定系统 PowerShell 路径、固定嵌入脚本；路径/参数只是 stdin JSON，不是脚本文本。
	cmd := exec.CommandContext(ctx, s.shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encodedScript(taskScript))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = bytes.NewReader(data)
	cmd.WaitDelay = time.Second
	out := &limitedOutput{limit: 2 << 20}
	stderr := &limitedOutput{limit: 32 << 10}
	cmd.Stdout = out
	cmd.Stderr = stderr
	if err = cmd.Run(); err != nil {
		return TaskValue{}, fmt.Errorf("restricted legacy task operation: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var t TaskValue
	if err = json.Unmarshal(out.Bytes(), &t); err != nil {
		return TaskValue{}, fmt.Errorf("decode task response: %w", err)
	}
	if t.Name != r.Name {
		return TaskValue{}, ErrConflict
	}
	return t, nil
}
func (s *WindowsTaskStore) ReadTask(ctx context.Context, name string) (TaskValue, error) {
	return s.call(ctx, taskRequest{Operation: "read", Name: name})
}
func (s *WindowsTaskStore) SetTaskEnabled(ctx context.Context, name string, before TaskValue, enabled bool) (TaskValue, error) {
	p := KnownLegacyPolicy(s.account, s.executables)
	checker := &LegacyManager{policy: p}
	if !checker.ownsTask(name, before) {
		return TaskValue{}, ErrNotOwned
	}
	return s.call(ctx, taskRequest{Operation: "toggle", Name: name, ExpectedXML: before.XML, ExpectedEnabled: before.Enabled, Enabled: enabled})
}
