package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"toolnest/internal/network"
)

type fixture struct {
	t                *testing.T
	manager          *Manager
	server           *httptest.Server
	release          Release
	archive          []byte
	private          ed25519.PrivateKey
	requests         atomic.Int32
	downloadRequests atomic.Int32
	mode             atomic.Int32
	gate             *LocalGate
}

func TestNetworkPolicyAndCancellationAreMutuallyExclusive(t *testing.T) {
	fixture := newFixture(t)
	release, err := fixture.manager.HoldNetworkPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Check(context.Background(), true); !errors.Is(err, ErrBusy) {
		t.Fatal("request raced policy change", err)
	}
	release()
	ctx, err := fixture.manager.beginNetwork(context.Background(), Downloading)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.HoldNetworkPolicy(); !errors.Is(err, ErrBusy) {
		t.Fatal("policy changed during request", err)
	}
	if !fixture.manager.CancelNetwork() {
		t.Fatal("request could not be cancelled")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal(ctx.Err())
	}
	fixture.manager.end(ctx.Err())
	if fixture.manager.Status().Phase != Unchecked {
		t.Fatal(fixture.manager.Status())
	}
	if fixture.manager.CancelNetwork() {
		t.Fatal("cancelled a nonexistent request")
	}
}

func signedArchive(t *testing.T, version, executable string, content []byte) []byte {
	t.Helper()
	descriptor, err := json.Marshal(Descriptor{Version: version, Target: "windows-amd64", Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	return archiveEntries(t, map[string][]byte{DescriptorName: descriptor, executable: content})
}
func archiveEntries(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	for name, content := range entries {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0700, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func newFixture(t *testing.T) *fixture {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &fixture{t: t, private: private, gate: &LocalGate{}}
	fixture.archive = signedArchive(t, "1.1.0", "ToolNest.exe", []byte("signed new version"))
	digest := sha256.Sum256(fixture.archive)
	fixture.release = Release{Version: "1.1.0", Size: int64(len(fixture.archive)), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, digest[:]))}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		fixture.requests.Add(1)
		switch req.URL.Path {
		case "/manifest":
			if fixture.mode.Load() == 4 {
				writer.WriteHeader(503)
				return
			}
			if err := json.NewEncoder(writer).Encode(fixture.release); err != nil {
				t.Error(err)
			}
		case "/notes":
			fmt.Fprint(writer, "User-visible release notes")
		case "/redirect":
			http.Redirect(writer, req, fixture.server.URL+"/archive", 302)
		case "/archive":
			fixture.downloadRequests.Add(1)
			switch fixture.mode.Load() {
			case 1:
				writer.Header().Set("Content-Length", fmt.Sprint(len(fixture.archive)+12))
				_, _ = writer.Write(fixture.archive[:len(fixture.archive)/2])
				if hijacker, ok := writer.(http.Hijacker); ok {
					connection, _, err := hijacker.Hijack()
					if err == nil {
						connection.Close()
					}
				}
			case 2:
				writer.Header().Set("Content-Length", fmt.Sprint(len(fixture.archive)))
				writer.WriteHeader(200)
				if flusher, ok := writer.(http.Flusher); ok {
					flusher.Flush()
				}
				<-req.Context().Done()
			case 3:
				_, _ = writer.Write(append(append([]byte(nil), fixture.archive...), 0))
			default:
				_, _ = writer.Write(fixture.archive)
			}
		case "/delta":
			fmt.Fprint(writer, "not a valid delta")
		default:
			writer.WriteHeader(404)
		}
	}))
	t.Cleanup(fixture.server.Close)
	fixture.release.URL = fixture.server.URL + "/redirect"
	fixture.release.NotesURL = fixture.server.URL + "/notes"
	source, err := HTTPTestSource(fixture.server.URL + "/manifest")
	if err != nil {
		t.Fatal(err)
	}
	networkManager, err := network.New(context.Background(), network.Config{Mode: network.Direct}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { networkManager.Snapshot().CloseIdleConnections() })
	root := t.TempDir()
	install := filepath.Join(root, "program")
	state := filepath.Join(root, "state")
	if err := os.Mkdir(install, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install, "ToolNest.exe"), []byte("usable previous version"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture.manager, err = New(Config{CurrentVersion: "1.0.0", Source: source, PublicKey: base64.StdEncoding.EncodeToString(public), InstallDir: install, StateDir: state, Network: networkManager, Gate: fixture.gate})
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}
func (fixture *fixture) prepare() *Prepared {
	fixture.t.Helper()
	if release, err := fixture.manager.Check(context.Background(), false); err != nil || release == nil {
		fixture.t.Fatalf("check failed: %v", err)
	}
	prepared, err := fixture.manager.Prepare(context.Background())
	if err != nil {
		fixture.t.Fatal(err)
	}
	return prepared
}
func (fixture *fixture) assertOld() {
	fixture.t.Helper()
	data, err := os.ReadFile(filepath.Join(fixture.manager.config.InstallDir, "ToolNest.exe"))
	if err != nil || string(data) != "usable previous version" {
		fixture.t.Fatalf("old executable was not retained: %v", err)
	}
}

func TestUnconfiguredAndStableVersions(t *testing.T) {
	manager, err := New(Config{CurrentVersion: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if manager.Status().Phase != Unconfigured {
		t.Fatal("missing production settings looked ready")
	}
	if _, err = manager.Check(context.Background(), false); !errors.Is(err, ErrNotConfigured) {
		t.Fatal(err)
	}
	for _, version := range []string{"1.0.0-beta.1", "1.0", "01.2.3", "1.2.3+", "1.2.3+x..y", "1.2.3.4", "1.2.18446744073709551616"} {
		if _, err := stableVersion(version); err == nil {
			t.Errorf("accepted %s", version)
		}
	}
	if compare, err := compareVersions("v1.20.0+build.1", "1.9.99"); err != nil || compare != 1 {
		t.Fatal("semantic comparison failed")
	}
	if _, err := HTTPTestSource("http://localhost/manifest"); err == nil {
		t.Fatal("DNS name accepted by isolated test source")
	}
	if _, err := HTTPTestSource("http://192.0.2.1/manifest"); err == nil {
		t.Fatal("remote HTTP source accepted")
	}
	if _, err := GitHub("owner/repo"); err != nil {
		t.Fatal(err)
	}
	if _, err := GitHub("../secret/repo"); err == nil {
		t.Fatal("invalid repository accepted")
	}
}

func TestCheckPrepareDoNotApplyAndPersist(t *testing.T) {
	fixture := newFixture(t)
	prepared := fixture.prepare()
	fixture.assertOld()
	status := fixture.manager.Status()
	if status.Phase != Ready || status.AvailableVersion != "1.1.0" || status.Notes != "User-visible release notes" || status.Downloaded != fixture.release.Size || status.Total != fixture.release.Size || status.LastCheck.IsZero() {
		t.Fatalf("incomplete status: %+v", status)
	}
	if fixture.downloadRequests.Load() != 1 {
		t.Fatal("download was not redirected")
	}
	manager, err := New(fixture.manager.config)
	if err != nil {
		t.Fatal(err)
	}
	if manager.Status().Phase != Ready || manager.Prepared().SHA256 != prepared.SHA256 {
		t.Fatal("prepared update not restored on next startup")
	}
	fixture.release.Version = "1.0.0"
	release, err := fixture.manager.Check(context.Background(), true)
	if err != nil || release != nil || fixture.manager.Status().Phase != Ready {
		t.Fatal("checking an older release must preserve the ready update")
	}
}

func TestBadSignatureDisconnectOversizeAndCancellation(t *testing.T) {
	t.Run("wrong-key", func(t *testing.T) {
		fixture := newFixture(t)
		_, other, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(fixture.archive)
		fixture.release.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(other, hash[:]))
		if _, err := fixture.manager.Check(context.Background(), false); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.manager.Prepare(context.Background()); !errors.Is(err, ErrSignature) {
			t.Fatal(err)
		}
		fixture.assertOld()
		if fixture.manager.Status().Phase != Failed {
			t.Fatal("signature failure not visible")
		}
	})
	for name, mode := range map[string]int32{"disconnect": 1, "oversize": 3} {
		t.Run(name, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.mode.Store(mode)
			if _, err := fixture.manager.Check(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.manager.Prepare(context.Background()); err == nil {
				t.Fatal("corrupt download accepted")
			}
			fixture.assertOld()
			if fixture.manager.Prepared() != nil {
				t.Fatal("bad payload marked prepared")
			}
		})
	}
	t.Run("cancel", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.mode.Store(2)
		if _, err := fixture.manager.Check(context.Background(), false); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if _, err := fixture.manager.Prepare(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		fixture.assertOld()
	})
	t.Run("offline-check", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.mode.Store(4)
		if _, err := fixture.manager.Check(context.Background(), false); err == nil {
			t.Fatal("offline check looked successful")
		}
		if fixture.manager.Status().Phase == Latest {
			t.Fatal("offline looked up-to-date")
		}
	})
}

func TestDuplicateDownloadAndBusyApply(t *testing.T) {
	fixture := newFixture(t)
	fixture.prepare()
	release, err := fixture.gate.TryAcquire("backup")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Apply(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if fixture.manager.Status().Phase != Waiting {
		t.Fatal("busy switch not visible")
	}
	fixture.assertOld()
	release()
	fixture = newFixture(t)
	if _, err := fixture.manager.Check(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	fixture.mode.Store(2)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := fixture.manager.Prepare(ctx); done <- err }()
	deadline := time.Now().Add(2 * time.Second)
	for fixture.downloadRequests.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, err := fixture.manager.Prepare(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatal("duplicate download allowed")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSignedDescriptorAndSingleExecutableArchive(t *testing.T) {
	for name, content := range map[string]func(*testing.T) []byte{
		"wrong-version": func(t *testing.T) []byte { return signedArchive(t, "9.0.0", "ToolNest.exe", []byte("bad")) },
		"multi-file": func(t *testing.T) []byte {
			return archiveEntries(t, map[string][]byte{"ToolNest.exe": []byte("exe"), "sidecar.dll": []byte("dll")})
		},
		"traversal":          func(t *testing.T) []byte { return archiveEntries(t, map[string][]byte{"../escape.exe": []byte("bad")}) },
		"no-version-binding": func(t *testing.T) []byte { return archiveEntries(t, map[string][]byte{"ToolNest.exe": []byte("bad")}) },
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.archive = content(t)
			digest := sha256.Sum256(fixture.archive)
			fixture.release.Size = int64(len(fixture.archive))
			fixture.release.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(fixture.private, digest[:]))
			if _, err := fixture.manager.Check(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.manager.Prepare(context.Background()); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			fixture.assertOld()
		})
	}
}

func TestDeltaFallbackNeverAcceptsUnsignedOutput(t *testing.T) {
	for _, adapter := range []DeltaApplier{nil, func(_ context.Context, _, _, destination, _, _ string) error {
		return os.WriteFile(destination, []byte("invalid output"), 0600)
	}} {
		fixture := newFixture(t)
		fixture.manager.config.ApplyDelta = adapter
		delta := []byte("not a valid delta")
		digest := sha256.Sum256(delta)
		fixture.release.Deltas = []Delta{{From: "1.0.0", URL: fixture.server.URL + "/delta", Size: int64(len(delta)), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(fixture.private, digest[:]))}}
		fixture.prepare()
		status := fixture.manager.Status()
		if status.UsingDelta || status.FallbackReason == "" || fixture.downloadRequests.Load() != 1 {
			t.Fatal("delta did not fall back to signed full archive")
		}
	}
}

func TestApplyPreservesOldAndConfirmsStartup(t *testing.T) {
	fixture := newFixture(t)
	fixture.prepare()
	result, err := fixture.manager.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(result.Backup)
	if err != nil || string(old) != "usable previous version" {
		t.Fatal("old backup missing")
	}
	config := fixture.manager.config
	config.CurrentVersion = "1.1.0"
	newManager, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := newManager.ConfirmStartup(context.Background(), strings.Repeat("0", 64)); !errors.Is(err, ErrStartup) {
		t.Fatal("forged startup acknowledgement accepted")
	}
	process := newFakeProcess()
	defer process.stop()
	err = fixture.manager.RestartInstalled(context.Background(), result, []string{"--tray"}, time.Second, func(executable string, args []string) (Process, error) {
		if executable != result.Executable || args[0] != "--tray" {
			t.Fatal("restart context lost")
		}
		if err := newManager.ConfirmStartup(context.Background(), result.Token); err != nil {
			t.Fatal(err)
		}
		return process, nil
	})
	if err != nil || fixture.manager.Status().Phase != Confirmed {
		t.Fatalf("confirmation failed: %v", err)
	}
	if _, err := os.Stat(result.Backup); err != nil {
		t.Fatal("confirmed update removed old version")
	}
}

type fakeProcess struct {
	done    chan struct{}
	once    sync.Once
	killErr error
}

func newFakeProcess() *fakeProcess       { return &fakeProcess{done: make(chan struct{})} }
func (process *fakeProcess) Wait() error { <-process.done; return errors.New("process exited") }
func (process *fakeProcess) Kill() error {
	if process.killErr != nil {
		return process.killErr
	}
	process.stop()
	return nil
}
func (process *fakeProcess) stop() { process.once.Do(func() { close(process.done) }) }

func TestStartupFailureRollbackAndBlockedVersion(t *testing.T) {
	for _, failure := range []string{"spawn", "early-exit", "timeout", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.prepare()
			result, err := fixture.manager.Apply(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			process := newFakeProcess()
			defer process.stop()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			launch := func(string, []string) (Process, error) {
				switch failure {
				case "spawn":
					return nil, errors.New("OS refused executable")
				case "early-exit":
					process.stop()
				case "cancel":
					cancel()
				}
				return process, nil
			}
			err = fixture.manager.RestartInstalled(ctx, result, nil, 75*time.Millisecond, launch)
			if failure == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrStartup) {
				t.Fatal(err)
			}
			fixture.assertOld()
			if _, err := fixture.manager.Check(context.Background(), false); !errors.Is(err, ErrBlocked) {
				t.Fatal("failed version would loop automatically")
			}
			if _, err := fixture.manager.Check(context.Background(), true); !errors.Is(err, ErrBlocked) {
				t.Fatal("ordinary manual check unblocked a failed release")
			}
			if _, err := fixture.manager.RetryBlocked(context.Background()); err != nil {
				t.Fatal("explicit retry was not allowed", err)
			}
		})
	}
}

func TestOccupiedAndRollbackFailure(t *testing.T) {
	t.Run("occupied", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.prepare()
		fixture.manager.rename = func(string, string) error { return os.ErrPermission }
		if _, err := fixture.manager.Apply(context.Background()); !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
		fixture.assertOld()
	})
	t.Run("candidate-move-failure", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.prepare()
		count := 0
		fixture.manager.rename = func(from, to string) error {
			count++
			if count == 2 {
				return os.ErrPermission
			}
			return os.Rename(from, to)
		}
		if _, err := fixture.manager.Apply(context.Background()); !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
		fixture.assertOld()
	})
	t.Run("rollback-failure", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.prepare()
		result, err := fixture.manager.Apply(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		fixture.manager.rename = func(from, to string) error {
			if from == result.Backup {
				return os.ErrPermission
			}
			return os.Rename(from, to)
		}
		err = fixture.manager.RestartInstalled(context.Background(), result, nil, time.Second, func(string, []string) (Process, error) { return nil, errors.New("spawn failed") })
		if !errors.Is(err, ErrRecovery) || !strings.Contains(err.Error(), result.Backup) {
			t.Fatal("recovery failure did not expose retained backup")
		}
		if _, err := os.Stat(result.Backup); err != nil {
			t.Fatal("backup lost")
		}
	})
	t.Run("cannot-stop-new-process", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.prepare()
		result, err := fixture.manager.Apply(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		process := newFakeProcess()
		process.killErr = os.ErrPermission
		defer process.stop()
		err = fixture.manager.RestartInstalled(context.Background(), result, nil, 30*time.Millisecond, func(string, []string) (Process, error) { return process, nil })
		if !errors.Is(err, ErrRecovery) {
			t.Fatal(err)
		}
		if _, err := os.Stat(result.Backup); err != nil {
			t.Fatal("backup lost")
		}
	})
}

func TestInterruptedSwitchAndPreparedTampering(t *testing.T) {
	t.Run("recover-pending", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.prepare()
		if _, err := fixture.manager.Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := fixture.manager.Recover(context.Background()); !errors.Is(err, ErrStartup) {
			t.Fatal(err)
		}
		fixture.assertOld()
	})
	t.Run("tampered-staging", func(t *testing.T) {
		fixture := newFixture(t)
		prepared := fixture.prepare()
		if err := os.WriteFile(prepared.Executable, []byte("tampered"), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.manager.Apply(context.Background()); !errors.Is(err, ErrSignature) {
			t.Fatal(err)
		}
		fixture.assertOld()
	})
	t.Run("forged-prepared-digest", func(t *testing.T) {
		fixture := newFixture(t)
		prepared := fixture.prepare()
		if err := os.WriteFile(prepared.Executable, []byte("tampered"), 0700); err != nil {
			t.Fatal(err)
		}
		digest, err := fileDigest(prepared.Executable, fixture.manager.config.MaxExecutable)
		if err != nil {
			t.Fatal(err)
		}
		fixture.manager.prepared.SHA256 = digest
		result, err := fixture.manager.Apply(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(result.Executable)
		if err != nil || string(data) != "signed new version" {
			t.Fatal("editable staging hash authorized unsigned code")
		}
	})
}

func TestUpdateRequestsShareAuthenticatedProxy(t *testing.T) {
	fixture := newFixture(t)
	var proxyCalls atomic.Int32
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		proxyCalls.Add(1)
		if req.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pass")) {
			writer.WriteHeader(407)
			return
		}
		copy := req.Clone(req.Context())
		copy.RequestURI = ""
		copy.Header.Del("Proxy-Authorization")
		response, err := transport.RoundTrip(copy)
		if err != nil {
			writer.WriteHeader(502)
			return
		}
		defer response.Body.Close()
		for name, values := range response.Header {
			for _, value := range values {
				writer.Header().Add(name, value)
			}
		}
		writer.WriteHeader(response.StatusCode)
		_, _ = io.Copy(writer, response.Body)
	}))
	defer proxy.Close()
	if err := fixture.manager.config.Network.Configure(context.Background(), network.Config{Mode: network.Custom, Address: proxy.URL, CredentialID: "test"}, testVault{}); err != nil {
		t.Fatal(err)
	}
	fixture.prepare()
	if proxyCalls.Load() != 4 {
		t.Fatalf("manifest/notes/redirect/archive did not all use proxy: %d", proxyCalls.Load())
	}
}

type testVault struct{}

func (testVault) Read(context.Context, string) (network.Credentials, error) {
	return network.Credentials{Username: "user", Password: "pass"}, nil
}
func (testVault) Write(context.Context, string, network.Credentials) error { return nil }
func (testVault) Delete(context.Context, string) error                     { return nil }

func TestHelperCommandDoesNotRestart(t *testing.T) {
	fixture := newFixture(t)
	fixture.prepare()
	command, err := fixture.manager.HelperCommand(context.Background(), []string{"--tray"})
	if err != nil {
		t.Fatal(err)
	}
	if len(command.Arguments) != 3 || command.Arguments[0] != "--toolnest-update-helper" {
		t.Fatal("missing main helper contract")
	}
	fixture.assertOld()
	if fixture.manager.Status().Phase != Ready {
		t.Fatal("helper preparation switched automatically")
	}
}

func TestCrashBoundaryRecovery(t *testing.T) {
	for _, boundary := range []string{"staged-before-move", "staged-after-move", "old-moved", "rollback-before-journal"} {
		t.Run(boundary, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.prepare()
			result, err := fixture.manager.Apply(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var journal transaction
			if err := readJSON(fixture.manager.journalPath(), &journal); err != nil {
				t.Fatal(err)
			}
			switch boundary {
			case "staged-before-move":
				if err := os.Rename(journal.Target, journal.Candidate); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(journal.Backup, journal.Target); err != nil {
					t.Fatal(err)
				}
				journal.Phase = "staged"
			case "staged-after-move", "old-moved":
				if err := os.Rename(journal.Target, journal.Candidate); err != nil {
					t.Fatal(err)
				}
				journal.Phase = "staged"
				if boundary == "old-moved" {
					journal.Phase = "old-moved"
				}
			case "rollback-before-journal":
				if err := os.Rename(journal.Target, filepath.Join(fixture.manager.config.InstallDir, ".failed-test.exe")); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(journal.Backup, journal.Target); err != nil {
					t.Fatal(err)
				}
			}
			if err := writeJSON(fixture.manager.journalPath(), journal); err != nil {
				t.Fatal(err)
			}
			err = fixture.manager.Recover(context.Background())
			if boundary == "staged-before-move" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrStartup) {
				t.Fatalf("unexpected recovery error at %s: %v, backup=%s", boundary, err, result.Backup)
			}
			fixture.assertOld()
		})
	}
}

func TestReleaseMutationCannotRedirectPreparedUpdate(t *testing.T) {
	fixture := newFixture(t)
	release, err := fixture.manager.Check(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	release.URL = "http://127.0.0.1:1/not-authorized"
	release.Version = "9.9.9"
	prepared, err := fixture.manager.Prepare(context.Background())
	if err != nil || prepared.Version != "1.1.0" {
		t.Fatal("caller mutated pinned update", err)
	}
}

func TestArchiveSymlinkDuplicateAndExecutableLimit(t *testing.T) {
	for _, kind := range []string{"symlink", "duplicate", "expanded-limit"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newFixture(t)
			var buffer bytes.Buffer
			compressed := gzip.NewWriter(&buffer)
			writer := tar.NewWriter(compressed)
			descriptor, _ := json.Marshal(Descriptor{Version: "1.1.0", Target: "windows-amd64", Executable: "ToolNest.exe"})
			if err := writer.WriteHeader(&tar.Header{Name: DescriptorName, Mode: 0600, Size: int64(len(descriptor))}); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(descriptor); err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				if err := writer.WriteHeader(&tar.Header{Name: "ToolNest.exe", Typeflag: tar.TypeSymlink, Linkname: "outside.exe"}); err != nil {
					t.Fatal(err)
				}
			} else {
				for index := 0; index < 2; index++ {
					if err := writer.WriteHeader(&tar.Header{Name: "ToolNest.exe", Mode: 0700, Size: 4}); err != nil {
						t.Fatal(err)
					}
					if _, err := writer.Write([]byte("code")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := compressed.Close(); err != nil {
				t.Fatal(err)
			}
			fixture.archive = buffer.Bytes()
			hash := sha256.Sum256(fixture.archive)
			fixture.release.Size = int64(len(fixture.archive))
			fixture.release.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(fixture.private, hash[:]))
			if kind == "expanded-limit" {
				fixture.manager.config.MaxExecutable = 2
			}
			if _, err := fixture.manager.Check(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.manager.Prepare(context.Background()); err == nil {
				t.Fatal("unsafe signed archive accepted")
			}
			fixture.assertOld()
		})
	}
}

func TestFailedUpdateTargetChangedIsNotOverwritten(t *testing.T) {
	fixture := newFixture(t)
	fixture.prepare()
	result, err := fixture.manager.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(result.Executable, []byte("another application wrote this"), 0700); err != nil {
		t.Fatal(err)
	}
	err = fixture.manager.RestartInstalled(context.Background(), result, nil, time.Second, func(string, []string) (Process, error) {
		t.Fatal("changed executable must not launch")
		return nil, nil
	})
	if !errors.Is(err, ErrSignature) {
		t.Fatal(err)
	}
	if _, err := os.Stat(result.Backup); err != nil {
		t.Fatal("old backup lost")
	}
}

func TestBlockedReleaseCannotReapplyFromPersistedPreparation(t *testing.T) {
	fixture := newFixture(t)
	fixture.prepare()
	result, err := fixture.manager.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = fixture.manager.RestartInstalled(context.Background(), result, nil, time.Second, func(string, []string) (Process, error) { return nil, os.ErrPermission }); !errors.Is(err, ErrStartup) {
		t.Fatal(err)
	}
	if fixture.manager.Prepared() != nil {
		t.Fatal("failed release remained queued for next startup")
	}
	manager, err := New(fixture.manager.config)
	if err != nil {
		t.Fatal(err)
	}
	if manager.Prepared() != nil {
		t.Fatal("failed release was reloaded for automatic activation")
	}
	if _, err := manager.Check(context.Background(), false); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if _, err := manager.Check(context.Background(), true); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if _, err := manager.Prepare(context.Background()); !errors.Is(err, ErrBlocked) {
		t.Fatal("download bypassed blocked-release confirmation", err)
	}
	if _, err := manager.RetryBlocked(context.Background()); err != nil {
		t.Fatal(err)
	}
	manager, err = New(fixture.manager.config)
	if err != nil || manager.Prepared() == nil {
		t.Fatal("explicit retry did not authorize next-startup activation", err)
	}
}
