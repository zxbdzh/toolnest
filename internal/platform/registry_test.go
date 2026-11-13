package platform

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
	"toolnest/internal/core"
)

type memoryStore struct {
	state     core.State
	writes    int
	failAfter bool
}

func (m *memoryStore) read(context.Context) (core.State, error) { return cloneState(m.state), nil }
func (m *memoryStore) write(_ context.Context, s core.Scope, v core.Value) error {
	m.writes++
	if s == core.User {
		m.state.User = v
	} else {
		m.state.System = v
	}
	if m.failAfter {
		return errors.New("injected write failure")
	}
	return nil
}
func (m *memoryStore) notify(context.Context) error { return nil }
func sampleState() core.State {
	return core.State{User: core.Value{Exists: true, Type: 1, Bytes: []byte{65, 0, 0, 0}}, System: core.Value{Exists: true, Type: 2, Bytes: []byte{37, 0, 65, 0, 37, 0, 0, 0}}}
}
func samplePlan(before core.State, scopes ...core.Scope) core.RestorePlan {
	target := cloneState(before)
	target.User.Bytes = []byte{66, 0}
	target.System.Bytes = []byte{67, 0}
	return core.RestorePlan{Token: core.NewID(), Before: cloneState(before), Target: core.NewSnapshot(target, "manual", time.Now()), Scopes: scopes}
}
func memoryRegistry(m *memoryStore) *Registry { return &Registry{store: m, sid: "S-1-5-21-1-2-3-1001"} }

func TestConfirmationRequiredAndScopeExact(t *testing.T) {
	m := &memoryStore{state: sampleState()}
	r := memoryRegistry(m)
	ctx := context.Background()
	if _, err := r.Authorize(ctx, []core.Scope{core.User}); !errors.Is(err, ErrNotConfirmed) {
		t.Fatal(err)
	}
	p := samplePlan(m.state, core.User)
	if err := r.ConfirmRestore(p); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Authorize(ctx, []core.Scope{core.System}); !errors.Is(err, ErrNotConfirmed) {
		t.Fatal(err)
	}
	if m.writes != 0 {
		t.Fatal("unconfirmed write")
	}
}
func TestCancelledSystemAuthorizationHasZeroWrites(t *testing.T) {
	m := &memoryStore{state: sampleState()}
	r := memoryRegistry(m)
	p := samplePlan(m.state, core.User, core.System)
	r.start = func(_ context.Context, a authorization) (helperSession, error) {
		if a.OriginalSID != r.sid {
			t.Fatal("original SID changed")
		}
		return nil, ErrCancelled
	}
	if err := r.ConfirmRestore(p); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Authorize(context.Background(), p.Scopes); !errors.Is(err, ErrCancelled) {
		t.Fatal(err)
	}
	if err := r.Write(context.Background(), core.User, p.Target.User); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	if m.writes != 0 || !m.state.Equal(p.Before) {
		t.Fatal("cancelled request mutated registry")
	}
}
func TestConfirmedValueImmutableAndOneShotRollback(t *testing.T) {
	m := &memoryStore{state: sampleState()}
	r := memoryRegistry(m)
	p := samplePlan(m.state, core.User)
	ctx := context.Background()
	if err := r.ConfirmRestore(p); err != nil {
		t.Fatal(err)
	}
	p.Target.User.Bytes[0] = 99 // UI 草稿的后续变化不能修改授权目标。
	release, err := r.Authorize(ctx, p.Scopes)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err = r.Write(ctx, core.User, p.Target.User); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	target := core.Value{Exists: true, Type: 1, Bytes: []byte{66, 0}}
	if err = r.Write(ctx, core.User, target); err != nil {
		t.Fatal(err)
	}
	if err = r.Write(ctx, core.User, r.active.Before.User); err != nil {
		t.Fatal(err)
	}
	if err = r.Write(ctx, core.User, target); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("replayed authorization", err)
	}
	if m.writes != 2 {
		t.Fatal(m.writes)
	}
	release()
	release()
	if err = r.Write(ctx, core.User, target); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
}
func TestChangedBeforeOrDuringAuthorizationStops(t *testing.T) {
	for _, during := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "during"}[during], func(t *testing.T) {
			m := &memoryStore{state: sampleState()}
			r := memoryRegistry(m)
			p := samplePlan(m.state, core.User)
			ctx := context.Background()
			if err := r.ConfirmRestore(p); err != nil {
				t.Fatal(err)
			}
			if during {
				release, err := r.Authorize(ctx, p.Scopes)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			m.state.User.Bytes = []byte{90, 0}
			var err error
			if during {
				err = r.Write(ctx, core.User, p.Target.User)
			} else {
				_, err = r.Authorize(ctx, p.Scopes)
			}
			if !errors.Is(err, core.ErrChanged) || m.writes != 0 {
				t.Fatal(err, m.writes)
			}
		})
	}
}
func TestInvalidScopesAndTestRootsRejected(t *testing.T) {
	for _, s := range []string{`..\Environment`, `Software\Environment`, "AABB", "a/b"} {
		if (Options{TestRoot: s}).validate() == nil {
			t.Fatal(s)
		}
	}
	m := &memoryStore{state: sampleState()}
	r := memoryRegistry(m)
	for _, scopes := range [][]core.Scope{nil, {core.User, core.User}, {"invalid"}} {
		if r.ConfirmRestore(samplePlan(m.state, scopes...)) == nil {
			t.Fatal(scopes)
		}
	}
}
func TestFrameStrictnessAndBounds(t *testing.T) {
	var round bytes.Buffer
	if err := writeFrame(&round, helperReply{Ready: true}); err != nil {
		t.Fatal(err)
	}
	var reply helperReply
	if err := readFrame(&round, &reply); err != nil || !reply.Ready {
		t.Fatal(err)
	}
	for _, payload := range []string{`{"operation":"write","key":"evil"}`, `{"operation":"close"} {}`} {
		var b bytes.Buffer
		var header [4]byte
		binary.LittleEndian.PutUint32(header[:], uint32(len(payload)))
		b.Write(header[:])
		b.WriteString(payload)
		if readFrame(&b, new(helperCommand)) == nil {
			t.Fatal("accepted extra fields/data")
		}
	}
	for _, size := range []uint32{0, maxFrame + 1} {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], size)
		if readFrame(bytes.NewReader(b[:]), new(helperCommand)) == nil {
			t.Fatal(size)
		}
	}
}
func TestHelperRestrictsCommandsAndRollback(t *testing.T) {
	for _, mode := range []string{"rollback", "command", "user-scope", "wrong-value", "external-change", "write-failure-rollback"} {
		t.Run(mode, func(t *testing.T) {
			before := sampleState()
			p := samplePlan(before, core.User, core.System)
			a := authorization{Version: 1, OriginalSID: "original", Token: p.Token, Before: before, Target: p.Target.State, Scopes: p.Scopes}
			m := &memoryStore{state: cloneState(before)}
			client, server := net.Pipe()
			defer client.Close()
			_ = client.SetDeadline(time.Now().Add(3 * time.Second))
			done := make(chan error, 1)
			go func() { defer server.Close(); done <- serveWrites(context.Background(), server, a, m) }()
			send := func(c helperCommand) helperReply {
				t.Helper()
				if err := writeFrame(client, c); err != nil {
					t.Fatal(err)
				}
				var r helperReply
				if err := readFrame(client, &r); err != nil {
					t.Fatal(err)
				}
				return r
			}
			command := helperCommand{Operation: "write", Scope: core.System, Value: &a.Target.System}
			switch mode {
			case "command":
				command.Operation = "exec"
			case "user-scope":
				command.Scope = core.User
			case "wrong-value":
				v := core.Value{Exists: true, Type: 1, Bytes: []byte{1, 2}}
				command.Value = &v
			case "external-change":
				m.state.System.Bytes = []byte{88, 0}
			case "write-failure-rollback":
				m.failAfter = true
			}
			r := send(command)
			if mode == "rollback" || mode == "write-failure-rollback" {
				if mode == "rollback" && r.Error != "" {
					t.Fatal(r.Error)
				}
				m.failAfter = false
				r = send(helperCommand{Operation: "write", Scope: core.System, Value: &a.Before.System})
				if r.Error != "" {
					t.Fatal(r.Error)
				}
				r = send(command)
				if r.Error == "" {
					t.Fatal("accepted second forward write")
				}
			} else {
				if r.Error == "" {
					t.Fatal("unsafe command accepted")
				}
				if mode == "external-change" {
					client.Close()
				}
			}
			client.Close()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("helper leaked")
			}
			if mode == "rollback" || mode == "write-failure-rollback" {
				if m.writes != 2 || !m.state.Equal(before) {
					t.Fatal(m.writes, m.state)
				}
			} else if m.writes != 0 {
				t.Fatal("unsafe mutation")
			}
		})
	}
}

type cancelledReadyHelper struct{ closed bool }

func (h *cancelledReadyHelper) write(context.Context, core.Scope, core.Value) error {
	return errors.New("不应写入")
}
func (h *cancelledReadyHelper) close() { h.closed = true }

func TestCancelAfterHelperReadyStillHasZeroWrites(t *testing.T) {
	m := &memoryStore{state: sampleState()}
	r := memoryRegistry(m)
	p := samplePlan(m.state, core.User, core.System)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &cancelledReadyHelper{}
	r.start = func(context.Context, authorization) (helperSession, error) { cancel(); return h, nil }
	if err := r.ConfirmRestore(p); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Authorize(ctx, p.Scopes); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !h.closed || m.writes != 0 || r.active != nil {
		t.Fatal("最后确认取消仍留下授权")
	}
	if _, err := r.Authorize(context.Background(), p.Scopes); !errors.Is(err, ErrNotConfirmed) {
		t.Fatal("取消后重复使用确认", err)
	}
}
func TestHiddenConfirmationTokenRejected(t *testing.T) {
	m := &memoryStore{state: sampleState()}
	r := memoryRegistry(m)
	for _, token := range []string{"shown\x00hidden", "line\nsecond", "line\rsecond"} {
		p := samplePlan(m.state, core.User)
		p.Token = token
		if r.ConfirmRestore(p) == nil {
			t.Fatal("接受不可见确认字段")
		}
	}
}

var _ io.ReadWriter = (*bytes.Buffer)(nil)
