package e2e

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"debug/pe"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"toolnest/internal/network"
	"toolnest/internal/update"
)

type publicUpdatePolicy struct {
	Repository             string `json:"repository"`
	Channel                string `json:"channel"`
	PublicKey              string `json:"publicKey"`
	BaselineVersion        string `json:"baselineVersion"`
	TargetVersion          string `json:"targetVersion"`
	AuthenticodeConfigured bool   `json:"authenticodeConfigured"`
}

func readPublicUpdatePolicy(t *testing.T) publicUpdatePolicy {
	t.Helper()
	data, err := os.ReadFile("TestUpdateSource.json")
	if err != nil {
		t.Fatal(err)
	}
	var policy publicUpdatePolicy
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestPublicUpdateSourcePolicyPinsRealTestIdentity(t *testing.T) {
	policy := readPublicUpdatePolicy(t)
	key, err := base64.StdEncoding.DecodeString(policy.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		t.Fatal("public update policy must pin an Ed25519 public key, never a private key")
	}
	if policy.Repository != "zxbdzh/toolnest-updates-test" || policy.Channel != "test" || policy.BaselineVersion != "0.1.0" || policy.TargetVersion != "0.1.1" || policy.AuthenticodeConfigured {
		t.Fatal("approved public test source identity or unsigned Windows disclosure changed")
	}
	if _, err := update.GitHub(policy.Repository); err != nil {
		t.Fatal(err)
	}
}

func TestOptionalPublicGitHubUpdateStaging(t *testing.T) {
	keyFile := os.Getenv("TOOLNEST_PUBLIC_TEST_KEY_FILE")
	if keyFile == "" {
		t.Skip("explicit independently trusted public test key not supplied; ordinary tests never contact the public update repository")
	}
	policy := readPublicUpdatePolicy(t)
	repository := policy.Repository
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(key)) != policy.PublicKey {
		t.Fatal("independently supplied public key differs from the approved test source policy")
	}
	expected, err := os.ReadFile(os.Getenv("TOOLNEST_PUBLIC_TEST_EXPECTED_EXE"))
	if err != nil {
		t.Fatal(err)
	}
	proxyAddress := os.Getenv("TOOLNEST_PUBLIC_TEST_PROXY")
	proxy, err := url.Parse(proxyAddress)
	if err != nil || net.ParseIP(proxy.Hostname()) == nil || !net.ParseIP(proxy.Hostname()).IsLoopback() {
		t.Fatal("public update acceptance requires an explicit numeric loopback proxy")
	}
	manager, err := network.New(context.Background(), network.Config{Mode: network.Custom, Address: proxyAddress}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Snapshot().CloseIdleConnections()
	source, err := update.GitHub(repository)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	install := filepath.Join(root, "programs")
	if err := os.Mkdir(install, 0700); err != nil {
		t.Fatal(err)
	}
	old := []byte("MZ-SYNTHETIC-0.1.0-NO-EXECUTION")
	installed := filepath.Join(install, "ToolNest.exe")
	if err := os.WriteFile(installed, old, 0600); err != nil {
		t.Fatal(err)
	}
	updater, err := update.New(update.Config{CurrentVersion: "0.1.0", Source: source, PublicKey: strings.TrimSpace(string(key)),
		InstallDir: install, StateDir: filepath.Join(root, "updates"), Executable: "ToolNest.exe", Network: manager, Gate: &update.LocalGate{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := updater.Check(ctx, true); err != nil {
		t.Fatal(err)
	}
	status := updater.Status()
	if status.Phase != update.Available || status.AvailableVersion != "0.1.1" || !strings.Contains(status.Notes, "验收") {
		t.Fatalf("unexpected public release metadata: %+v", status)
	}
	prepared, err := updater.Prepare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(prepared.Executable)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(payload) != sha256.Sum256(expected) {
		t.Fatal("verified public release differs from the independently built application")
	}
	image, err := pe.NewFile(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	header, ok := image.OptionalHeader.(*pe.OptionalHeader64)
	if !ok || header.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		t.Fatal("public update is not a Windows x64 GUI application")
	}
	current, err := os.ReadFile(installed)
	if err != nil || !bytes.Equal(current, old) || updater.Status().Phase != update.Ready {
		t.Fatal("public staging changed the application before explicit restart consent")
	}
	// 只在临时目录下载并验签，不执行新版、不调用 Apply，也不触碰已安装程序和真实 PATH。
	detail, _ := json.Marshal(map[string]any{"repository": repository, "from": "0.1.0", "available": prepared.Version,
		"phase": updater.Status().Phase, "executableMatches": true, "windowsGUI": true, "restartPerformed": false, "installedApplicationChanged": false})
	t.Log(string(detail))
}
