package lifecycle

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf16"
)

const (
	RunString        uint32 = 1 // REG_SZ only; REG_EXPAND_SZ is not silently expanded or claimed.
	ToolNestRunName         = "ToolNest"
	LegacyRunName           = "PathGuard"
	LegacyTaskMarker        = "PathGuard/RegistryPathBackup/v1/49b84af9-b152-49f9-9de1-6ee0d93adfd6"
)

// RunValue preserves absence, registry type and exact UTF-16 bytes for safe undo.
type RunValue struct {
	Exists bool
	Type   uint32
	Data   []byte
}

func (v RunValue) Equal(other RunValue) bool {
	return v.Exists == other.Exists && v.Type == other.Type && bytes.Equal(v.Data, other.Data)
}
func (v RunValue) clone() RunValue { v.Data = bytes.Clone(v.Data); return v }

// RunStore is current-user-only. Replace must re-read and refuse a changed expected value.
// It is not a cross-process transaction; implementations must also verify the written value.
type RunStore interface {
	ReadRun(context.Context, string) (RunValue, error)
	ReplaceRun(context.Context, string, RunValue, RunValue) error
}

// Account is a verified current-user identity, never a caller-selected alternate HKCU account.
type Account struct {
	SID  string
	Name string
}

// Command binds a fully qualified executable and an exact argument vector. No environment expansion.
type Command struct {
	Executable string
	Args       []string
}

func validateExe(s string) error {
	if strings.ContainsAny(s, "\"\x00\r\n") || strings.Contains(s, "%") || len(s) < 4 {
		return errors.New("invalid executable path")
	}
	// 只接受明确盘符绝对路径或 UNC；相对路径与盘符相对路径不能成为归属依据。
	if !((len(s) > 3 && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) && s[1] == ':' && (s[2] == '\\' || s[2] == '/')) || (strings.HasPrefix(s, "\\\\") && len(strings.Split(strings.TrimPrefix(s, "\\\\"), "\\")) >= 3)) {
		return errors.New("executable must be an absolute Windows path")
	}
	return nil
}
func (c Command) validate() error {
	if err := validateExe(c.Executable); err != nil {
		return err
	}
	for _, a := range c.Args {
		if strings.ContainsAny(a, "\x00\r\n") {
			return errors.New("invalid command argument")
		}
	}
	return nil
}

func quoteArg(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	n := 0
	for _, r := range s {
		if r == '\\' {
			n++
			continue
		}
		if r == '"' {
			b.WriteString(strings.Repeat("\\", 2*n+1))
		} else {
			b.WriteString(strings.Repeat("\\", n))
		}
		n = 0
		b.WriteRune(r)
	}
	b.WriteString(strings.Repeat("\\", 2*n))
	b.WriteByte('"')
	return b.String()
}
func (c Command) Value() (RunValue, error) {
	if err := c.validate(); err != nil {
		return RunValue{}, err
	}
	parts := []string{quoteArg(c.Executable)}
	for _, a := range c.Args {
		parts = append(parts, quoteArg(a))
	}
	u := utf16.Encode([]rune(strings.Join(parts, " ") + "\x00"))
	data := make([]byte, len(u)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(data[2*i:], v)
	}
	return RunValue{true, RunString, data}, nil
}
func decodeRun(v RunValue) (string, error) {
	if !v.Exists || v.Type != RunString || len(v.Data) < 2 || len(v.Data)%2 != 0 {
		return "", ErrNotOwned
	}
	u := make([]uint16, len(v.Data)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(v.Data[i*2:])
	}
	if u[len(u)-1] != 0 {
		return "", ErrNotOwned
	}
	u = u[:len(u)-1]
	for i := 0; i < len(u); i++ {
		if u[i] == 0 {
			return "", ErrNotOwned
		}
		if u[i] >= 0xD800 && u[i] <= 0xDBFF {
			if i+1 >= len(u) || u[i+1] < 0xDC00 || u[i+1] > 0xDFFF {
				return "", ErrNotOwned
			}
			i++
		} else if u[i] >= 0xDC00 && u[i] <= 0xDFFF {
			return "", ErrNotOwned
		}
	}
	return string(utf16.Decode(u)), nil
}
func (c Command) owns(v RunValue) bool {
	text, err := decodeRun(v)
	if err != nil {
		return false
	}
	argv, err := splitCommand(text)
	return err == nil && len(argv) > 0 && samePath(argv[0], c.Executable) && slices.Equal(argv[1:], c.Args)
}
func samePath(a, b string) bool {
	return strings.EqualFold(strings.ReplaceAll(a, "/", "\\"), strings.ReplaceAll(b, "/", "\\"))
}

// splitCommand follows Windows backslash/quote rules without invoking a shell.
func splitCommand(s string) ([]string, error) {
	var result []string
	for i := 0; i < len(s); {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i == len(s) {
			break
		}
		var b strings.Builder
		quoted := false
		for i < len(s) {
			if !quoted && (s[i] == ' ' || s[i] == '\t') {
				break
			}
			n := 0
			for i < len(s) && s[i] == '\\' {
				n++
				i++
			}
			if i < len(s) && s[i] == '"' {
				b.WriteString(strings.Repeat("\\", n/2))
				if n%2 == 1 {
					b.WriteByte('"')
					i++
				} else if quoted && i+1 < len(s) && s[i+1] == '"' {
					b.WriteByte('"')
					i += 2
				} else {
					quoted = !quoted
					i++
				}
				continue
			}
			b.WriteString(strings.Repeat("\\", n))
			if i == len(s) {
				break
			}
			if !quoted && (s[i] == ' ' || s[i] == '\t') {
				break
			}
			b.WriteByte(s[i])
			i++
		}
		if quoted {
			return nil, ErrNotOwned
		}
		result = append(result, b.String())
	}
	return result, nil
}

type UndoRecord struct {
	Version    int       `json:"version"`
	Account    Account   `json:"account"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	BeforeRun  RunValue  `json:"beforeRun"`
	AfterRun   RunValue  `json:"afterRun"`
	BeforeTask TaskValue `json:"beforeTask"`
	AfterTask  TaskValue `json:"afterTask"`
}

// Journal.Save must durably persist a copy before returning. Failure aborts the mutation.
// The caller retains BackupID for explicit undo; no automatic rollback overwrites newer changes.
type Journal interface {
	Save(context.Context, UndoRecord) (string, error)
}
type Change struct {
	BackupID string
	Record   UndoRecord
	Changed  bool
}

type RunManager struct {
	store   RunStore
	journal Journal
	account Account
	command Command
}

func NewRunManager(store RunStore, journal Journal, account Account, exe string) (*RunManager, error) {
	c := Command{exe, []string{AutostartArgument}}
	if store == nil || journal == nil || account.SID == "" {
		return nil, errors.New("Run store, journal and current SID required")
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &RunManager{store, journal, account, c}, nil
}

// Enabled reads actual system registration; constructing a manager never enables startup.
func (m *RunManager) Enabled(ctx context.Context) (bool, error) {
	v, e := m.store.ReadRun(ctx, ToolNestRunName)
	if e != nil {
		return false, e
	}
	if !v.Exists {
		return false, nil
	}
	if !m.command.owns(v) {
		return false, ErrNotOwned
	}
	return true, nil
}
func (m *RunManager) SetEnabled(ctx context.Context, enabled bool) (Change, error) {
	before, err := m.store.ReadRun(ctx, ToolNestRunName)
	if err != nil {
		return Change{}, err
	}
	if before.Exists && !m.command.owns(before) {
		return Change{}, ErrNotOwned
	}
	if before.Exists == enabled {
		return Change{}, nil
	}
	after := RunValue{}
	if enabled {
		after, err = m.command.Value()
		if err != nil {
			return Change{}, err
		}
	}
	return changeRun(ctx, m.store, m.journal, m.account, ToolNestRunName, before, after)
}
func changeRun(ctx context.Context, s RunStore, j Journal, a Account, name string, before, after RunValue) (Change, error) {
	// 预览与提交分开：备份前重读，写入处再比较，外部更改绝不主动覆盖。
	now, err := s.ReadRun(ctx, name)
	if err != nil {
		return Change{}, err
	}
	if !now.Equal(before) {
		return Change{}, ErrConflict
	}
	record := UndoRecord{Version: 1, Account: a, Kind: "run", Name: name, BeforeRun: before.clone(), AfterRun: after.clone()}
	id, err := j.Save(ctx, record)
	r := Change{BackupID: id, Record: record}
	if err != nil {
		return r, fmt.Errorf("save undo backup: %w", err)
	}
	err = s.ReplaceRun(ctx, name, before, after)
	if err == nil {
		r.Changed = true
	}
	return r, err
}
func (m *RunManager) Undo(ctx context.Context, r UndoRecord) (Change, error) {
	if r.Version != 1 || r.Kind != "run" || r.Name != ToolNestRunName || r.Account.SID != m.account.SID || (!r.BeforeRun.Exists && !r.AfterRun.Exists) || r.BeforeRun.Exists && !m.command.owns(r.BeforeRun) || r.AfterRun.Exists && !m.command.owns(r.AfterRun) {
		return Change{}, ErrNotOwned
	}
	return changeRun(ctx, m.store, m.journal, m.account, r.Name, r.AfterRun, r.BeforeRun)
}
