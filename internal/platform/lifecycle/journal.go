package lifecycle

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileJournal stores private undo records; it never touches Run, PATH or Task Scheduler.
// Choose a current-user application-data directory, not an executable or snapshot directory.
type FileJournal struct{ directory string }

func NewFileJournal(directory string) (*FileJournal, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("undo directory must be absolute")
	}
	return &FileJournal{filepath.Clean(directory)}, nil
}
func (j *FileJournal) checkDirectory() error {
	// 拒绝可见的链接/重解析路径，避免日志被引向非预期位置；同用户竞态不作原子保证。
	for p := j.directory; ; p = filepath.Dir(p) {
		s, e := os.Lstat(p)
		if e == nil {
			if !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
				return errors.New("undo directory is not a plain directory")
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		next := filepath.Dir(p)
		if next == p {
			break
		}
	}
	return nil
}
func (j *FileJournal) Save(ctx context.Context, r UndoRecord) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := j.checkDirectory(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(j.directory, 0700); err != nil {
		return "", err
	}
	if err := j.checkDirectory(); err != nil {
		return "", err
	}
	data, err := json.Marshal(cloneRecord(r))
	if err != nil {
		return "", err
	}
	if len(data) > 4<<20 {
		return "", errors.New("undo record too large")
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(random[:])
	path := filepath.Join(j.directory, id+".json")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	// 只有落盘并可读的备份才能作为系统修改前置条件。
	check, err := j.Load(ctx, id)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(check)
	if err != nil || string(encoded) != string(data) {
		return "", errors.New("undo backup readback mismatch")
	}
	ok = true
	return id, nil
}
func (j *FileJournal) Load(ctx context.Context, id string) (UndoRecord, error) {
	if err := ctx.Err(); err != nil {
		return UndoRecord{}, err
	}
	if len(id) != 32 || strings.ToLower(id) != id {
		return UndoRecord{}, errors.New("invalid undo backup ID")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return UndoRecord{}, err
	}
	if err := j.checkDirectory(); err != nil {
		return UndoRecord{}, err
	}
	path := filepath.Join(j.directory, id+".json")
	s, err := os.Lstat(path)
	if err != nil {
		return UndoRecord{}, err
	}
	if !s.Mode().IsRegular() || s.Size() > 4<<20 {
		return UndoRecord{}, errors.New("invalid undo backup file")
	}
	f, err := os.Open(path)
	if err != nil {
		return UndoRecord{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil {
		return UndoRecord{}, err
	}
	if len(data) > 4<<20 {
		return UndoRecord{}, errors.New("undo backup exceeds limit")
	}
	var r UndoRecord
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&r); err != nil {
		return r, err
	}
	if dec.Decode(new(any)) != io.EOF {
		return r, errors.New("trailing undo data")
	}
	if r.Version != 1 {
		return r, fmt.Errorf("unsupported undo record version %d", r.Version)
	}
	return r, nil
}
