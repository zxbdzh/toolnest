package app

// View 是原生界面的只读数据副本；后台写入不能直接修改绘制中的状态。
type View struct {
	Version             string
	User, System        Scope
	Snapshots           []Snapshot
	Records             []Record
	Settings            Settings
	Update              Update
	Busy, Status, Error string
	NextRun             string
	LegacyCount         int
	LegacyEntries       []LegacyEntry
	TrayAvailable       bool
	ProxyTesting        bool
	PendingExit         string
	ExitCommitting      bool
	SettingsRevision    uint64
	LastPlanResult      string
	RetryAt             string
	Result              OperationResult
	Problems            []Problem
	Isolated            bool
}

type Scope struct {
	Exists    bool
	Type      string
	Text      string
	Entries   []Entry
	RawDigest string
	RawSize   int
}

type Entry struct {
	Index int
	Text  string
}
type Snapshot struct {
	ID, CreatedAt, Source, Summary string
	User, System                   Scope
	Error                          string
}
type Record struct {
	At, FinishedAt, Operation, Trigger, Outcome, Message, SnapshotID string
	Result                                                           *OperationResult `json:"result,omitempty"`
}

type ScopeResult struct{ Name, Status string }
type OperationResult struct {
	ID, Operation, Outcome, Title, Message, Details string
	SnapshotID, ProtectionID                        string
	Scopes                                          []ScopeResult
}
type Problem struct{ Code, Title, Message, Details, Action string }
type LegacyEntry struct {
	ID, Kind, Description string
	Owned, Disabled       bool
}
type Change struct{ Kind, Scope, Before, After string }
type Preview struct {
	Token, SnapshotID string
	User, System      bool
	Changes           []Change
}

type Settings struct {
	Theme                                             string
	CloseBehavior                                     string
	LaunchAtLogin, StartInTray, OnboardingDone        bool
	SnapshotDir                                       string
	ScheduleEnabled                                   bool
	ScheduleMode                                      string
	IntervalHours, DailyHour, DailyMinute             int
	ProxyMode, ProxyURL, ProxyUsername, ProxyPassword string
	AutoCheck, AutoDownload                           bool
}

type Update struct {
	State, Current, Available, Notes, LastCheck, Error string
	Progress                                           float64
	NetworkPhase, ErrorCode, RecoveryPath, PreparedAt  string
	Details                                            string
	SourceURL                                          string
	DismissedVersion, NewerVersion                     string
	Downloaded, Total                                  int64
	BytesPerSecond                                     float64
	RolledBack                                         bool
}

// Actions 隔离 UI 与系统写入；耗时操作由应用层串行执行，UI 只提交意图。
type Actions interface {
	View() View
	Refresh()
	Backup()
	PreviewRestore(id string, user, system bool) (Preview, error)
	Restore(preview Preview)
	DeleteSnapshots(ids []string)
	SaveSettings(settings Settings)
	CompleteOnboarding(settings Settings, skip bool)
	ImportLegacy()
	MoveSnapshots(directory string)
	DisableLegacy(id string)
	UndoLegacy(id string)
	CheckUpdate()
	DownloadUpdate()
	RestartUpdate()
	TestProxy(settings Settings)
	Copy(text string)
	ChooseDirectory() (string, error)
	OpenDataDirectory()
	RequestClose(behavior string, remember bool)
	Quit()
}
