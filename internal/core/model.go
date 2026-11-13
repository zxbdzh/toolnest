package core

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf16"
)

const MaxPathBytes = 1024 * 1024
const MaxSnapshotBytes = 16 * 1024 * 1024
const FormatVersion = 1

type Scope string

const (
	User   Scope = "user"
	System Scope = "system"
)

type Value struct {
	Exists bool   `json:"exists"`
	Type   uint32 `json:"registry_type"`
	Bytes  []byte `json:"bytes"`
}

func (value Value) MarshalJSON() ([]byte, error) {
	type rawValue Value
	if value.Bytes == nil {
		value.Bytes = []byte{}
	}
	return json.Marshal(rawValue(value))
}

func (value Value) Equal(other Value) bool {
	return value.Exists == other.Exists && value.Type == other.Type && bytes.Equal(value.Bytes, other.Bytes)
}

func (value Value) Validate() error {
	if !value.Exists {
		if value.Type != 0 || len(value.Bytes) != 0 {
			return errors.New("缺值不能携带类型或字节")
		}
		return nil
	}
	if value.Type != 1 && value.Type != 2 {
		return fmt.Errorf("不支持的注册表类型 %d", value.Type)
	}
	if len(value.Bytes) > MaxPathBytes {
		return errors.New("PATH 超出大小上限")
	}
	return nil
}

func (value Value) TypeName() string {
	if !value.Exists {
		return "不存在"
	}
	if value.Type == 2 {
		return "REG_EXPAND_SZ"
	}
	return "REG_SZ"
}

func (value Value) Text() string {
	units := make([]uint16, (len(value.Bytes)+1)/2)
	for index := range units {
		units[index] = uint16(value.Bytes[index*2])
		if index*2+1 < len(value.Bytes) {
			units[index] |= uint16(value.Bytes[index*2+1]) << 8
		}
	}
	// 终止符只影响展示，原始字节始终独立保留，包含异常 UTF-16。
	if len(units) > 0 && units[len(units)-1] == 0 {
		units = units[:len(units)-1]
	}
	return string(utf16.Decode(units))
}

func (value Value) Entries() []string {
	if !value.Exists {
		return nil
	}
	return strings.Split(value.Text(), ";")
}

type State struct {
	User   Value `json:"user"`
	System Value `json:"system"`
}

func (state State) Equal(other State) bool {
	return state.User.Equal(other.User) && state.System.Equal(other.System)
}
func (state State) Value(scope Scope) Value {
	if scope == System {
		return state.System
	}
	return state.User
}
func (state State) Validate() error {
	return errors.Join(state.User.Validate(), state.System.Validate())
}

type Snapshot struct {
	Version   int       `json:"format_version"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Source    string    `json:"reason"`
	State
	Origin      string `json:"origin,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
}

func NewID() string {
	var identifier [16]byte
	if _, err := rand.Read(identifier[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(identifier[:])
}

func NewSnapshot(state State, source string, now time.Time) Snapshot {
	return Snapshot{Version: FormatVersion, ID: NewID(), CreatedAt: now.UTC(), Source: source, State: state}
}

func (snapshot Snapshot) Validate() error {
	if snapshot.Version != 0 && snapshot.Version != FormatVersion {
		return errors.New("未知快照版本")
	}
	if snapshot.ID == "" || len(snapshot.ID) > 160 || strings.ContainsAny(snapshot.ID, "/\\:\x00") || snapshot.ID == "." || snapshot.ID == ".." {
		return errors.New("快照标识不安全")
	}
	if snapshot.CreatedAt.IsZero() {
		return errors.New("快照缺少创建时间")
	}
	if !slices.Contains([]string{"manual", "automatic", "catch-up", "retry", "before-restore", "imported"}, snapshot.Source) {
		return errors.New("快照来源不支持")
	}
	return snapshot.State.Validate()
}

// DecodeSnapshot 显式检查字段存在性，避免坏文件被解码成“删除 PATH”。
func DecodeSnapshot(data []byte) (Snapshot, error) {
	if len(data) > MaxSnapshotBytes {
		return Snapshot{}, errors.New("快照文件超限")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Snapshot{}, err
	}
	for _, name := range []string{"id", "created_at", "reason", "user", "system"} {
		if _, ok := fields[name]; !ok {
			return Snapshot{}, fmt.Errorf("快照缺少字段 %s", name)
		}
	}
	var snapshot Snapshot
	if version, exists := fields["format_version"]; exists {
		if err := json.Unmarshal(version, &snapshot.Version); err != nil || snapshot.Version != FormatVersion {
			return snapshot, errors.New("未知快照版本")
		}
	} else {
		// Rust 旧格式的 Vec<u8> 是数字数组；新格式 []byte 使用 base64。
		for _, scope := range []string{"user", "system"} {
			if bytes.Equal(bytes.TrimSpace(fields[scope]), []byte("null")) {
				fields[scope] = json.RawMessage(`{"exists":false,"registry_type":0,"bytes":""}`)
				continue
			}
			var old struct {
				Type  *uint32  `json:"registry_type"`
				Bytes *[]uint8 `json:"bytes"`
			}
			decoder := json.NewDecoder(bytes.NewReader(fields[scope]))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&old); err != nil || old.Type == nil || old.Bytes == nil {
				return snapshot, errors.New("旧快照范围不完整")
			}
			converted, _ := json.Marshal(Value{Exists: true, Type: *old.Type, Bytes: *old.Bytes})
			fields[scope] = converted
		}
		data, _ = json.Marshal(fields)
	}
	if snapshot.Version != 0 {
		for _, scope := range []string{"user", "system"} {
			var fieldsScope map[string]json.RawMessage
			if err := json.Unmarshal(fields[scope], &fieldsScope); err != nil {
				return snapshot, err
			}
			for _, name := range []string{"exists", "registry_type", "bytes"} {
				field, ok := fieldsScope[name]
				if !ok || bytes.Equal(bytes.TrimSpace(field), []byte("null")) {
					return snapshot, fmt.Errorf("%s 缺少 %s", scope, name)
				}
			}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, snapshot.Validate()
}

type Entry struct {
	Index int
	Text  string
}

func Filter(value Value, query string) []Entry {
	var result []Entry
	for index, text := range value.Entries() {
		if strings.Contains(strings.ToLower(text), strings.ToLower(query)) {
			result = append(result, Entry{Index: index + 1, Text: text})
		}
	}
	return result
}

type Change struct {
	Kind          string
	Scope         Scope
	Before, After string
}

func Diff(before, after State, scopes []Scope) []Change {
	var changes []Change
	for _, scope := range scopes {
		old, next := before.Value(scope), after.Value(scope)
		if old.Equal(next) {
			continue
		}
		if old.Exists != next.Exists {
			changes = append(changes, Change{"存在性", scope, fmt.Sprint(old.Exists), fmt.Sprint(next.Exists)})
		}
		if old.Type != next.Type {
			changes = append(changes, Change{"类型", scope, old.TypeName(), next.TypeName()})
		}
		oldCounts, newCounts := map[string]int{}, map[string]int{}
		for _, text := range old.Entries() {
			oldCounts[text]++
		}
		for _, text := range next.Entries() {
			newCounts[text]++
		}
		seen := map[string]bool{}
		for _, text := range append(old.Entries(), next.Entries()...) {
			if seen[text] {
				continue
			}
			seen[text] = true
			if oldCounts[text] != newCounts[text] {
				changes = append(changes, Change{"条目数量", scope, fmt.Sprintf("%s × %d", text, oldCounts[text]), fmt.Sprintf("%s × %d", text, newCounts[text])})
			}
		}
		if !slices.Equal(old.Entries(), next.Entries()) {
			changes = append(changes, Change{"顺序／原始条目", scope, old.Text(), next.Text()})
		} else if !bytes.Equal(old.Bytes, next.Bytes) {
			changes = append(changes, Change{"原始字节", scope, fmt.Sprintf("%d 字节", len(old.Bytes)), fmt.Sprintf("%d 字节", len(next.Bytes))})
		}
	}
	return changes
}
