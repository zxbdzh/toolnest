package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Store struct{ Directory string }
type InvalidFile struct{ Name, Error string }
type Listing struct {
	Snapshots []Snapshot
	Invalid   []InvalidFile
}
type ImportResult struct {
	Copied, Skipped int
	Failed          []InvalidFile
}

func CheckDirectory(directory string) error {
	if !filepath.IsAbs(directory) {
		return errors.New("目录必须是绝对路径")
	}
	// 任一祖先为重解析点时拒绝，避免快照目录被重定向到未知位置。
	for current := filepath.Clean(directory); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("目录不能经过符号链接或重解析点")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return nil
}

func (store Store) Ensure() error {
	if err := CheckDirectory(store.Directory); err != nil {
		return err
	}
	return os.MkdirAll(store.Directory, 0700)
}

func (store Store) Load(id string) (Snapshot, error) {
	if id == "" || strings.ContainsAny(id, "/\\:\x00") || id == "." || id == ".." {
		return Snapshot{}, errors.New("快照标识不安全")
	}
	return LoadSnapshot(filepath.Join(store.Directory, id+".json"))
}

func LoadSnapshot(filename string) (Snapshot, error) {
	if err := CheckDirectory(filepath.Dir(filename)); err != nil {
		return Snapshot{}, err
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return Snapshot{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxSnapshotBytes {
		return Snapshot{}, errors.New("快照不是正常文件或超过大小限制")
	}
	file, err := os.Open(filename)
	if err != nil {
		return Snapshot{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxSnapshotBytes+1))
	if err != nil {
		return Snapshot{}, err
	}
	snapshot, err := DecodeSnapshot(data)
	if err != nil {
		return snapshot, err
	}
	if filepath.Base(filename) != snapshot.ID+".json" {
		return snapshot, errors.New("快照标识与文件名不一致")
	}
	return snapshot, nil
}

func (store Store) List() (Listing, error) {
	if err := CheckDirectory(store.Directory); err != nil {
		return Listing{}, err
	}
	entries, err := os.ReadDir(store.Directory)
	if err != nil {
		return Listing{}, fmt.Errorf("快照目录不可读取: %w", err)
	}
	var result Listing
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		snapshot, err := LoadSnapshot(filepath.Join(store.Directory, entry.Name()))
		if err != nil {
			result.Invalid = append(result.Invalid, InvalidFile{entry.Name(), err.Error()})
			continue
		}
		result.Snapshots = append(result.Snapshots, snapshot)
	}
	sort.Slice(result.Snapshots, func(left, right int) bool {
		if result.Snapshots[left].CreatedAt.Equal(result.Snapshots[right].CreatedAt) {
			return result.Snapshots[left].ID > result.Snapshots[right].ID
		}
		return result.Snapshots[left].CreatedAt.After(result.Snapshots[right].CreatedAt)
	})
	return result, nil
}

func (store Store) Save(snapshot Snapshot) error {
	if err := snapshot.Validate(); err != nil {
		return err
	}
	snapshot.Version = FormatVersion
	if err := CheckDirectory(store.Directory); err != nil {
		return err
	}
	info, err := os.Stat(store.Directory)
	if err != nil {
		return fmt.Errorf("快照目录不可用，未创建替代目录: %w", err)
	}
	if !info.IsDir() {
		return errors.New("快照位置不是目录")
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	filename := filepath.Join(store.Directory, snapshot.ID+".json")
	if _, err := os.Lstat(filename); !errors.Is(err, os.ErrNotExist) {
		return errors.New("快照标识已经存在，拒绝覆盖")
	}
	if err := AtomicWrite(filename, data, false); err != nil {
		return err
	}
	loaded, err := store.Load(snapshot.ID)
	if err != nil {
		return fmt.Errorf("快照读回失败: %w", err)
	}
	if !loaded.State.Equal(snapshot.State) || !loaded.CreatedAt.Equal(snapshot.CreatedAt) {
		return errors.New("快照读回校验不一致")
	}
	return nil
}

// AtomicWrite 先同步同目录暂存文件，配置替换留下可恢复副本。
func AtomicWrite(filename string, data []byte, replace bool) error {
	if err := CheckDirectory(filepath.Dir(filename)); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(filename), ".toolnest-*.pending")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if !replace {
		// 排他发布不能依赖 NTFS 硬链接，用户可能选择不支持硬链接的备份磁盘。
		return publishFile(name, filename)
	}
	backup := filename + ".previous"
	if current, err := os.Lstat(filename); err == nil {
		if !current.Mode().IsRegular() {
			return errors.New("拒绝替换非正常配置文件")
		}
		old, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		previous, err := os.CreateTemp(filepath.Dir(filename), ".toolnest-backup-*.pending")
		if err != nil {
			return err
		}
		previousName := previous.Name()
		defer os.Remove(previousName)
		if _, err = previous.Write(old); err == nil {
			err = previous.Sync()
		}
		closeErr := previous.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if err := replaceFile(previousName, backup); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return replaceFile(name, filename)
}

func (store Store) Import(directory string) (ImportResult, error) {
	listing, err := (Store{Directory: directory}).List()
	if err != nil {
		return ImportResult{}, err
	}
	result := ImportResult{Failed: listing.Invalid}
	for _, snapshot := range listing.Snapshots {
		if snapshot.Version == 0 && snapshot.Origin == "" {
			snapshot.Origin = "PathGuard:" + snapshot.ID
		}
		existing, loadErr := store.Load(snapshot.ID)
		if loadErr == nil {
			existing.Version = FormatVersion
			snapshot.Version = FormatVersion
			left, _ := json.Marshal(existing)
			right, _ := json.Marshal(snapshot)
			if bytes.Equal(left, right) {
				result.Skipped++
			} else {
				result.Failed = append(result.Failed, InvalidFile{snapshot.ID, "同标识内容冲突"})
			}
			continue
		}
		if !errors.Is(loadErr, os.ErrNotExist) {
			result.Failed = append(result.Failed, InvalidFile{snapshot.ID, loadErr.Error()})
			continue
		}
		if err := store.Save(snapshot); err != nil {
			result.Failed = append(result.Failed, InvalidFile{snapshot.ID, err.Error()})
		} else {
			result.Copied++
		}
	}
	return result, nil
}

func (store Store) CopyTo(directory string) (ImportResult, error) {
	source, err := filepath.Abs(store.Directory)
	if err != nil {
		return ImportResult{}, err
	}
	target, err := filepath.Abs(directory)
	if err != nil {
		return ImportResult{}, err
	}
	if strings.EqualFold(source, target) {
		return ImportResult{}, nil
	}
	for _, pair := range [][2]string{{source, target}, {target, source}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return ImportResult{}, errors.New("源与目标目录不能相互包含")
		}
	}
	destination := Store{Directory: target}
	if err := destination.Ensure(); err != nil {
		return ImportResult{}, err
	}
	result, err := destination.Import(source)
	if err != nil {
		return result, err
	}
	if len(result.Failed) != 0 {
		return result, fmt.Errorf("目录复制有 %d 项失败，继续使用原目录", len(result.Failed))
	}
	return result, nil
}
