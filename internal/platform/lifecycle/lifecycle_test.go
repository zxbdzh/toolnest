package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var testAccount = Account{SID: "S-1-5-21-100-200-300-1001", Name: `TEST\user`}

const testExe = `C:\Programs\ToolNest\toolnest.exe`
const oldExe = `C:\Old Apps\PathGuard\path-guard.exe`

func mustValue(t *testing.T, c Command) RunValue {
	t.Helper()
	v, e := c.Value()
	if e != nil {
		t.Fatal(e)
	}
	return v
}

type fakeRun struct {
	values        map[string]RunValue
	reads, writes int
	beforeReplace func()
}

func newFakeRun() *fakeRun { return &fakeRun{values: map[string]RunValue{}} }
func (f *fakeRun) ReadRun(ctx context.Context, n string) (RunValue, error) {
	if e := ctx.Err(); e != nil {
		return RunValue{}, e
	}
	f.reads++
	return f.values[n].clone(), nil
}
func (f *fakeRun) ReplaceRun(ctx context.Context, n string, b, a RunValue) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if f.beforeReplace != nil {
		f.beforeReplace()
	}
	if !f.values[n].Equal(b) {
		return ErrConflict
	}
	f.values[n] = a.clone()
	f.writes++
	return nil
}

type fakeJournal struct {
	records []UndoRecord
	err     error
	onSave  func()
}

func (f *fakeJournal) Save(ctx context.Context, r UndoRecord) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if f.err != nil {
		return "", f.err
	}
	f.records = append(f.records, cloneRecord(r))
	if f.onSave != nil {
		f.onSave()
	}
	return fmt.Sprint(len(f.records)), nil
}

type fakeTasks struct {
	tasks       map[string]TaskValue
	writes      int
	beforeWrite func()
}

func (f *fakeTasks) ReadTask(ctx context.Context, n string) (TaskValue, error) {
	if e := ctx.Err(); e != nil {
		return TaskValue{}, e
	}
	return f.tasks[n], nil
}
func (f *fakeTasks) SetTaskEnabled(ctx context.Context, n string, b TaskValue, e bool) (TaskValue, error) {
	if f.beforeWrite != nil {
		f.beforeWrite()
	}
	if !f.tasks[n].Equal(b) {
		return TaskValue{}, ErrConflict
	}
	a := b
	a.Enabled = e
	a.XML = strings.ReplaceAll(a.XML, fmt.Sprintf("<Enabled>%t</Enabled>", b.Enabled), fmt.Sprintf("<Enabled>%t</Enabled>", e))
	f.tasks[n] = a
	f.writes++
	return a, nil
}
func validTask() TaskValue {
	n := "PathGuard-PATH-Backup-" + testAccount.SID
	return TaskValue{Exists: true, Name: n, XML: `<Task xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Settings><Enabled>true</Enabled></Settings><Actions><Exec><Command>C:\Old Apps\PathGuard\path-guard.exe</Command></Exec></Actions></Task>`, Enabled: true, UserID: testAccount.SID, Author: "PathGuard:" + testAccount.SID, Source: LegacyTaskMarker, Description: LegacyTaskMarker, Executable: oldExe, Arguments: "--backup", ActionCount: 1, ActionType: 0, LogonType: 3, RunLevel: 0}
}
func legacyFixture(t *testing.T) (*LegacyManager, *fakeRun, *fakeTasks, *fakeJournal) {
	t.Helper()
	r := newFakeRun()
	r.values[LegacyRunName] = mustValue(t, Command{oldExe, []string{AutostartArgument}})
	task := validTask()
	ts := &fakeTasks{tasks: map[string]TaskValue{task.Name: task}}
	j := &fakeJournal{}
	m, e := NewLegacyManager(r, ts, j, KnownLegacyPolicy(testAccount, []string{oldExe}))
	if e != nil {
		t.Fatal(e)
	}
	return m, r, ts, j
}

func TestRunDefaultOffAndOwnership(t *testing.T) {
	r := newFakeRun()
	j := &fakeJournal{}
	m, e := NewRunManager(r, j, testAccount, testExe)
	if e != nil {
		t.Fatal(e)
	}
	on, e := m.Enabled(context.Background())
	if e != nil || on || r.writes != 0 {
		t.Fatalf("default changed: %v %v", on, e)
	}
	change, e := m.SetEnabled(context.Background(), true)
	if e != nil || !change.Changed || len(j.records) != 1 {
		t.Fatalf("enable: %+v %v", change, e)
	}
	if on, e = m.Enabled(context.Background()); !on || e != nil {
		t.Fatal(on, e)
	}
	if _, e = m.SetEnabled(context.Background(), true); e != nil || r.writes != 1 {
		t.Fatal(e, r.writes)
	}
	if _, e = m.Undo(context.Background(), change.Record); e != nil || r.values[ToolNestRunName].Exists {
		t.Fatal(e)
	}
	for _, v := range []RunValue{mustValue(t, Command{oldExe, []string{AutostartArgument}}), mustValue(t, Command{testExe, []string{"--other"}}), {Exists: true, Type: 2, Data: change.Record.AfterRun.Data}} {
		r.values[ToolNestRunName] = v
		writes := r.writes
		if _, e = m.SetEnabled(context.Background(), false); !errors.Is(e, ErrNotOwned) || r.writes != writes {
			t.Fatalf("unknown Run changed: %v", e)
		}
	}
}
func TestRunBackupFailureAndConcurrentEdits(t *testing.T) {
	for _, mode := range []string{"backup-fails", "changed-after-backup", "changed-at-replace"} {
		t.Run(mode, func(t *testing.T) {
			r := newFakeRun()
			j := &fakeJournal{}
			m, _ := NewRunManager(r, j, testAccount, testExe)
			foreign := mustValue(t, Command{oldExe, nil})
			switch mode {
			case "backup-fails":
				j.err = errors.New("disk full")
			case "changed-after-backup":
				j.onSave = func() { r.values[ToolNestRunName] = foreign }
			case "changed-at-replace":
				r.beforeReplace = func() { r.values[ToolNestRunName] = foreign }
			}
			if _, e := m.SetEnabled(context.Background(), true); e == nil || r.writes != 0 {
				t.Fatal(e, r.writes)
			}
		})
	}
}
func TestRunUndoNeverOverwritesLaterChanges(t *testing.T) {
	r := newFakeRun()
	j := &fakeJournal{}
	m, _ := NewRunManager(r, j, testAccount, testExe)
	change, e := m.SetEnabled(context.Background(), true)
	if e != nil {
		t.Fatal(e)
	}
	foreign := mustValue(t, Command{oldExe, nil})
	r.values[ToolNestRunName] = foreign
	if _, e = m.Undo(context.Background(), change.Record); !errors.Is(e, ErrConflict) || !r.values[ToolNestRunName].Equal(foreign) {
		t.Fatal(e)
	}
}
func TestCommandRoundTripAndStrictTypes(t *testing.T) {
	args := []string{"", "--autostart", "space value", `a\\`, `a\"b`, "中文参数"}
	c := Command{testExe, args}
	v := mustValue(t, c)
	if !c.owns(v) {
		t.Fatal("command did not round trip")
	}
	line, _ := decodeRun(v)
	parts, e := splitCommand(line)
	if e != nil || !slices.Equal(parts[1:], args) {
		t.Fatal(parts, e)
	}
	for _, exe := range []string{"toolnest.exe", `C:toolnest.exe`, `C:\bad".exe`, `%LOCALAPPDATA%\toolnest.exe`, "C:\bad\n.exe"} {
		if _, e := (Command{exe, nil}).Value(); e == nil {
			t.Fatalf("accepted %q", exe)
		}
	}
	for _, bad := range []RunValue{{true, 2, v.Data}, {true, 1, []byte{0x00}}, {true, 1, []byte{0x00, 0xD8, 0, 0}}, {true, 1, append(append([]byte(nil), v.Data...), 0, 0)}} {
		if c.owns(bad) {
			t.Fatal("accepted malformed/type-changed value")
		}
	}
	lower := Command{strings.ToLower(testExe), args}
	if !lower.owns(v) {
		t.Fatal("Windows path case should match")
	}
}

func TestLegacyUnknownNameAndOwnership(t *testing.T) {
	mutations := map[string]func(*TaskValue){"account": func(v *TaskValue) { v.UserID = "S-1-5-18" }, "exe": func(v *TaskValue) { v.Executable = `C:\Other\path-guard.exe` }, "args": func(v *TaskValue) { v.Arguments = "--backup --extra" }, "source": func(v *TaskValue) { v.Source = "" }, "author": func(v *TaskValue) { v.Author = "PathGuard" }, "description": func(v *TaskValue) { v.Description = "" }, "action-type": func(v *TaskValue) { v.ActionType = 5 }, "action-count": func(v *TaskValue) { v.ActionCount = 2 }, "logon": func(v *TaskValue) { v.LogonType = 1 }, "elevated": func(v *TaskValue) { v.RunLevel = 1 }}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			m, _, ts, _ := legacyFixture(t)
			v := validTask()
			mutate(&v)
			ts.tasks[v.Name] = v
			entries, e := m.Detect(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			entry := entries[1]
			if entry.Ownership != Unknown {
				t.Fatal(entry)
			}
			if _, e = m.Disable(context.Background(), entry); !errors.Is(e, ErrNotOwned) || ts.writes != 0 {
				t.Fatal(e)
			}
		})
	}
	m, r, _, _ := legacyFixture(t)
	r.values[LegacyRunName] = mustValue(t, Command{oldExe, []string{"--backup"}})
	entries, e := m.Detect(context.Background())
	if e != nil || entries[0].Ownership != Unknown {
		t.Fatal(entries, e)
	}
	p := KnownLegacyPolicy(testAccount, []string{oldExe})
	p.TaskNames = []string{"PathGuard"}
	if _, e = NewLegacyManager(r, m.tasks, m.journal, p); e == nil {
		t.Fatal("accepted unknown task name")
	}
}
func TestLegacyDisableAndDurableUndo(t *testing.T) {
	for _, kind := range []string{"run", "task"} {
		t.Run(kind, func(t *testing.T) {
			m, r, ts, j := legacyFixture(t)
			entries, e := m.Detect(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			index := 0
			if kind == "task" {
				index = 1
			}
			change, e := m.Disable(context.Background(), entries[index])
			if e != nil || !change.Changed || len(j.records) != 1 {
				t.Fatal(change, e)
			}
			// 使用写入前已持久化的记录，而不是变更返回值，验证应用重启后的撤销。
			if _, e = m.Undo(context.Background(), j.records[0]); e != nil {
				t.Fatal(e)
			}
			if kind == "task" {
				if !ts.tasks[entries[index].Name].Enabled {
					t.Fatal("task not restored")
				}
			} else if !r.values[LegacyRunName].Exists {
				t.Fatal("Run not restored")
			}
		})
	}
}
func TestLegacyPreviewAndUndoConflicts(t *testing.T) {
	m, _, ts, j := legacyFixture(t)
	entries, _ := m.Detect(context.Background())
	name := entries[1].Name
	v := ts.tasks[name]
	v.XML = strings.Replace(v.XML, "<Actions>", "<Actions Context=\"other\">", 1)
	ts.tasks[name] = v
	if _, e := m.Disable(context.Background(), entries[1]); !errors.Is(e, ErrConflict) || len(j.records) != 0 {
		t.Fatal(e)
	}
	ts.tasks[name] = validTask()
	entries, _ = m.Detect(context.Background())
	change, e := m.Disable(context.Background(), entries[1])
	if e != nil {
		t.Fatal(e)
	}
	v = ts.tasks[name]
	v.XML = strings.Replace(v.XML, "<Actions>", "<Actions Context=\"later\">", 1)
	ts.tasks[name] = v
	if _, e = m.Undo(context.Background(), change.Record); !errors.Is(e, ErrConflict) || ts.writes != 1 {
		t.Fatal(e)
	}
}
func TestLegacyBackupFailureAndFinalRecheck(t *testing.T) {
	for _, mode := range []string{"backup", "race"} {
		t.Run(mode, func(t *testing.T) {
			m, _, ts, j := legacyFixture(t)
			entries, _ := m.Detect(context.Background())
			if mode == "backup" {
				j.err = errors.New("disk full")
			} else {
				ts.beforeWrite = func() { v := ts.tasks[entries[1].Name]; v.Arguments = "--unknown"; ts.tasks[v.Name] = v }
			}
			if _, e := m.Disable(context.Background(), entries[1]); e == nil || ts.writes != 0 {
				t.Fatal(e)
			}
		})
	}
}
func TestTaskXMLComparesOnlySettingsEnabled(t *testing.T) {
	a := validTask()
	b := a
	b.Enabled = false
	b.XML = strings.Replace(a.XML, "<Enabled>true</Enabled>", "<Enabled>false</Enabled>", 1)
	if !sameTaskDefinition(a, b) {
		t.Fatal("Enabled-only change rejected")
	}
	b.XML = strings.Replace(b.XML, "<Command>", "<Command>changed", 1)
	if sameTaskDefinition(a, b) {
		t.Fatal("action XML ignored")
	}
	for _, s := range []string{"", "<Task></Task>", `<!DOCTYPE x><Task xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"/>`} {
		if _, e := stableTaskXML(s); e == nil {
			t.Fatal("bad XML accepted")
		}
	}
}
func TestFileJournalPersistsAndLimitsIDs(t *testing.T) {
	j, e := NewFileJournal(filepath.Join(t.TempDir(), "undo"))
	if e != nil {
		t.Fatal(e)
	}
	r := UndoRecord{Version: 1, Account: testAccount, Kind: "run", Name: ToolNestRunName, BeforeRun: mustValue(t, Command{testExe, []string{AutostartArgument}})}
	id, e := j.Save(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	loaded, e := j.Load(context.Background(), id)
	if e != nil || !loaded.BeforeRun.Equal(r.BeforeRun) {
		t.Fatal(loaded, e)
	}
	for _, id := range []string{"../escape", strings.Repeat("Z", 32), strings.Repeat("0", 31)} {
		if _, e := j.Load(context.Background(), id); e == nil {
			t.Fatal("invalid ID accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = j.Save(ctx, r); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

type fakeTray struct {
	available bool
	failed    bool
	closed    bool
	message   string
	err       error
}

func (t *fakeTray) Available() bool                 { return t.available && !t.closed }
func (t *fakeTray) SetState(s string, f bool) error { t.message = s; t.failed = f; return t.err }
func (t *fakeTray) Close()                          { t.closed = true }
func controllerFixture(t *testing.T) (*Controller, *[]string) {
	t.Helper()
	events := []string{}
	add := func(s string) func() { return func() { events = append(events, s) } }
	c, e := NewController(Hooks{Post: func(f func()) { f() }, ShowMain: add("show"), HideMain: add("hide"), OpenSettings: add("settings"), BackupNow: add("backup"), RequestExit: add("exit"), ReportError: func(error) { events = append(events, "error") }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(c.Close)
	return c, &events
}
func TestLaunchAndTrayFallback(t *testing.T) {
	cases := []struct {
		launch          Launch
		pref, available bool
		want            []string
	}{{ManualLaunch, true, true, []string{"show"}}, {AutostartLaunch, true, true, []string{"hide"}}, {AutostartLaunch, false, true, []string{"show"}}, {AutostartLaunch, true, false, []string{"show", "error"}}}
	for _, v := range cases {
		c, events := controllerFixture(t)
		tray := &fakeTray{available: v.available}
		c.Start(v.launch, v.pref, func(func(TrayAction)) (Tray, error) { return tray, nil })
		if !slices.Equal(*events, v.want) {
			t.Fatalf("got %v want %v", *events, v.want)
		}
	}
	if ParseLaunch([]string{AutostartArgument}) != AutostartLaunch || ParseLaunch([]string{AutostartArgument, "--helper"}) != ManualLaunch {
		t.Fatal("launch parsing")
	}
}
func TestControllerActionsAndTrayLoss(t *testing.T) {
	c, events := controllerFixture(t)
	tray := &fakeTray{available: true}
	c.Start(ManualLaunch, false, func(func(TrayAction)) (Tray, error) { return tray, nil })
	*events = nil
	c.SecondLaunch(AutostartLaunch)
	if len(*events) != 0 {
		t.Fatal("autostart disturbed UI")
	}
	c.SecondLaunch(ManualLaunch)
	for _, a := range []TrayAction{BackupAction, SettingsAction, ExitAction} {
		c.HandleAction(a)
	}
	if !slices.Equal(*events, []string{"show", "backup", "show", "settings", "exit"}) {
		t.Fatal(*events)
	}
	c.SetStatus("backup failed", true)
	if !tray.failed {
		t.Fatal("error state missing")
	}
	*events = nil
	tray.available = false
	c.Hide()
	if !slices.Equal(*events, []string{"show", "error"}) {
		t.Fatal(*events)
	}
	c.Close()
	if !tray.closed {
		t.Fatal("tray not released")
	}
	*events = nil
	c.HandleAction(ShowAction)
	if len(*events) != 0 {
		t.Fatal("callback after close")
	}
}
func TestUIQueueBuffersBeforeReady(t *testing.T) {
	q := NewUIQueue(func(f func()) { f() })
	defer q.Close()
	got := make(chan int, 2)
	q.Post(func() { got <- 1 })
	select {
	case <-got:
		t.Fatal("delivered before readiness")
	case <-time.After(20 * time.Millisecond):
	}
	q.Ready()
	q.Post(func() { got <- 2 })
	for _, want := range []int{1, 2} {
		select {
		case v := <-got:
			if v != want {
				t.Fatal(v, want)
			}
		case <-time.After(time.Second):
			t.Fatal("queued activation lost")
		}
	}
}
