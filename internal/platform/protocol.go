package platform

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"toolnest/internal/core"
)

const maxFrame = 6*core.MaxPathBytes + 16384

type helperCommand struct {
	Operation string      `json:"operation"`
	Scope     core.Scope  `json:"scope,omitempty"`
	Value     *core.Value `json:"value,omitempty"`
}
type helperReply struct {
	Error string `json:"error,omitempty"`
	Ready bool   `json:"ready,omitempty"`
}

func writeFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > maxFrame {
		return errors.New("助手请求超限")
	}
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(b)))
	if err := writeAll(w, length[:]); err != nil {
		return err
	}
	return writeAll(w, b)
}
func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
func readFrame(r io.Reader, v any) error {
	var length [4]byte
	if _, err := io.ReadFull(r, length[:]); err != nil {
		return err
	}
	n := binary.LittleEndian.Uint32(length[:])
	if n == 0 || n > maxFrame {
		return errors.New("助手消息大小不合法")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("助手消息包含额外内容")
	}
	return nil
}

// serveWrites 的能力只有本次系统 PATH：一次目标写入及一次原值回滚。
// 不接受命令、注册表键名、值名或任何未确认的数据。
func serveWrites(ctx context.Context, rw io.ReadWriter, a authorization, store rawStore) error {
	written := false
	rolledBack := false
	for {
		var c helperCommand
		if err := readFrame(rw, &c); err != nil {
			return err
		}
		if c.Operation == "close" && c.Scope == "" && c.Value == nil {
			return nil
		}
		var err error
		if c.Operation != "write" || c.Scope != core.System || c.Value == nil || !containsScope(a.Scopes, core.System) || rolledBack {
			err = ErrUnauthorized
		} else if err = c.Value.Validate(); err == nil {
			expected, next := a.Before.System, a.Target.System
			if written {
				expected, next = a.Target.System, a.Before.System
			}
			if !c.Value.Equal(next) {
				err = ErrUnauthorized
			} else {
				var current core.State
				current, err = store.read(ctx)
				if err == nil && !current.System.Equal(expected) {
					err = core.ErrChanged
				}
				if err == nil {
					err = store.write(ctx, core.System, *c.Value)
					if written {
						rolledBack = true
					} else {
						written = true
					}
				}
			}
		}
		reply := helperReply{}
		if err != nil {
			reply.Error = err.Error()
		}
		if e := writeFrame(rw, reply); e != nil {
			return e
		}
		if errors.Is(err, ErrUnauthorized) {
			return err
		}
	}
}
func containsScope(scopes []core.Scope, want core.Scope) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}
