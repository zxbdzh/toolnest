package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"toolnest/packaging"
)

func fixture(t *testing.T) (string, ed25519.PublicKey) {
	t.Helper()
	dir := t.TempDir()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "ToolNest.exe")
	if err = os.WriteFile(exe, []byte("MZ isolated artifact fixture, never executed"), 0600); err != nil {
		t.Fatal(err)
	}
	installer := "ToolNest-Setup-" + packaging.Version() + "-windows-amd64.exe"
	if err = os.WriteFile(filepath.Join(dir, installer), []byte("MZ installer test fixture, never executed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = writeJSON(filepath.Join(dir, "release-identity.json"), packaging.Identity{Version: packaging.Version(), Repository: "test-owner/toolnest-test", PublicKey: base64.StdEncoding.EncodeToString(pub), Channel: "test", RuntimeProtocol: 1}); err != nil {
		t.Fatal(err)
	}
	if err = bundle(dir, exe, "test-owner/toolnest-test", key, true, "# ToolNest "+packaging.Version()+"\n\n- 改善更新操作与结果反馈。"); err != nil {
		t.Fatal(err)
	}
	return dir, pub
}
func TestMyGoSignatureFormat(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	signature := sign(key, []byte("archive"))
	if !validSignature(pub, []byte("archive"), signature) || validSignature(pub, []byte("changed"), signature) {
		t.Fatal("signature digest mismatch")
	}
	raw, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || len(raw) != ed25519.SignatureSize {
		t.Fatal("not MyGo base64 Ed25519 format")
	}
}
func TestOfflineBundleAndProductionRejection(t *testing.T) {
	dir, pub := fixture(t)
	if err := verify(dir, "test-owner/toolnest-test", pub, true); err != nil {
		t.Fatal(err)
	}
	if err := verify(dir, "test-owner/toolnest-test", pub, false); err == nil || !strings.Contains(err.Error(), "test artifacts") {
		t.Fatalf("production accepted test: %v", err)
	}
}

func TestPublicPreviewNeedsSeparateExplicitVerification(t *testing.T) {
	dir, _ := fixture(t)
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	executable := filepath.Join(t.TempDir(), "ToolNest.exe")
	if err := os.WriteFile(executable, []byte("MZ public preview fixture, never executed"), 0600); err != nil {
		t.Fatal(err)
	}
	installer := "ToolNest-Setup-" + packaging.Version() + "-windows-amd64.exe"
	data, err := os.ReadFile(filepath.Join(dir, installer))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, installer), data, 0600); err != nil {
		t.Fatal(err)
	}
	identity := packaging.Identity{Version: packaging.Version(), Repository: "owner/public-toolnest", PublicKey: base64.StdEncoding.EncodeToString(pub), Channel: "public-preview", RuntimeProtocol: 1}
	if err := writeJSON(filepath.Join(output, "release-identity.json"), identity); err != nil {
		t.Fatal(err)
	}
	notes := "# ToolNest " + packaging.Version() + "\n\n- 首次公开体验版。"
	if err := bundleChannel(output, executable, identity.Repository, key, "public-preview", notes); err != nil {
		t.Fatal(err)
	}
	if err := verify(output, identity.Repository, pub, false); err == nil {
		t.Fatal("unsigned public preview passed production validation")
	}
	if err := verify(output, identity.Repository, pub, true); err == nil {
		t.Fatal("public preview silently entered historical test identity")
	}
	if err := verifyChannel(output, identity.Repository, pub, "public-preview"); err != nil {
		t.Fatal(err)
	}
}

func TestPublicPreviewKeygenNeverRotatesExistingIdentity(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(t.TempDir(), "public-identity")
	if err := run([]string{"keygen", "--public-preview", "--root", root, "--dir", directory}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "PUBLIC-PREVIEW")); err != nil {
		t.Fatal("missing distinct identity marker")
	}
	if _, err := os.Stat(filepath.Join(directory, "TEST-ONLY")); !os.IsNotExist(err) {
		t.Fatal("public identity marked as temporary fixture")
	}
	if err := run([]string{"keygen", "--public-preview", "--root", root, "--dir", directory}); err == nil {
		t.Fatal("existing update identity silently replaced")
	}
	if err := run([]string{"keygen", "--public-preview", "--test", "--root", root, "--dir", filepath.Join(t.TempDir(), "mixed")}); err == nil {
		t.Fatal("mixed release modes accepted")
	}
}
func TestRejectTamper(t *testing.T) {
	for _, name := range append(requiredNames(), "release-index.json", "release-index.json.sig", "SHA256SUMS") {
		t.Run(name, func(t *testing.T) {
			dir, pub := fixture(t)
			p := filepath.Join(dir, name)
			f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.WriteString("tamper")
			_ = f.Close()
			if verify(dir, "test-owner/toolnest-test", pub, true) == nil {
				t.Fatal("tampered artifact accepted")
			}
		})
	}
}
func TestRejectIncompleteAndUnexpected(t *testing.T) {
	for _, name := range requiredNames() {
		t.Run(name, func(t *testing.T) {
			dir, pub := fixture(t)
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
			if verify(dir, "test-owner/toolnest-test", pub, true) == nil {
				t.Fatal("missing artifact accepted")
			}
		})
	}
	dir, pub := fixture(t)
	_ = os.WriteFile(filepath.Join(dir, "private.key"), []byte("never publish keys"), 0600)
	if verify(dir, "test-owner/toolnest-test", pub, true) == nil {
		t.Fatal("unexpected artifact accepted")
	}
}
func TestKeygenRestrictions(t *testing.T) {
	root := t.TempDir()
	if generateTestKey(filepath.Join(root, "keys"), root) == nil {
		t.Fatal("in-repository keys accepted")
	}
	out := filepath.Join(t.TempDir(), "keys")
	if run([]string{"keygen", "--dir", out, "--root", root}) == nil {
		t.Fatal("implicit key generation accepted")
	}
	if err := run([]string{"keygen", "--test", "--dir", out, "--root", root}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "TEST-ONLY")); err != nil {
		t.Fatal(err)
	}
	if generateTestKey(out, root) == nil {
		t.Fatal("existing keys overwritten")
	}
}
func TestWrongKeyAndRepository(t *testing.T) {
	dir, pub := fixture(t)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if verify(dir, "test-owner/toolnest-test", other, true) == nil {
		t.Fatal("wrong trusted key accepted")
	}
	if verify(dir, "other/repository", pub, true) == nil {
		t.Fatal("wrong repository accepted")
	}
}

func TestVersionedUserReleaseNotes(t *testing.T) {
	for _, invalid := range []string{"", "# ToolNest 99.0.0\n- 错误版本", "# ToolNest " + packaging.Version() + "\n- TODO", "Verified Windows release"} {
		if validateNotes(invalid) == nil {
			t.Fatalf("invalid release notes accepted: %q", invalid)
		}
	}
	filename := filepath.Join(t.TempDir(), "notes.md")
	notes := "# ToolNest " + packaging.Version() + "\n\n- 普通退出后不会自动重启。"
	if err := os.WriteFile(filename, []byte(notes), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := releaseNotes(filename, "", false)
	if err != nil || loaded != notes {
		t.Fatal("release notes changed while loading", err)
	}
	if _, err := releaseNotes(filepath.Join(t.TempDir(), "missing.md"), "", false); err == nil {
		t.Fatal("production accepted missing notes")
	}
}
