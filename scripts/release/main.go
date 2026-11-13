// Command release creates/verifies local artifacts only. It never publishes.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"toolnest/packaging"
)

const mygoCommit = "0d644f4aca24be8208d1b7d364d7c928fc2cb890"
const goVersion = "go1.27.4"
const descriptorName = ".toolnest-release.json"

type manifest struct {
	Version   string `json:"version"`
	Notes     string `json:"notes,omitempty"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Signature string `json:"signature"`
}
type descriptor struct {
	Version    string `json:"version"`
	Target     string `json:"target"`
	Executable string `json:"executable"`
}
type artifact struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}
type inventory struct {
	Version    string     `json:"version"`
	Channel    string     `json:"channel"`
	Repository string     `json:"repository"`
	MyGoCommit string     `json:"mygoCommit"`
	GoVersion  string     `json:"goVersion"`
	Files      []artifact `json:"files"`
}

var stable = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("expected keygen, bundle or verify")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	dir := flags.String("dir", "", "artifact directory (or key output directory)")
	root := flags.String("root", "", "repository root, mandatory for keygen")
	exe := flags.String("exe", "", "application executable")
	repo := flags.String("repository", "", "GitHub owner/repository")
	pubText := flags.String("public-key", "", "trusted base64 public key")
	keyFile := flags.String("key-file", "", "external MyGo private key file")
	notesFile := flags.String("notes-file", "", "version-specific UTF-8 release notes")
	test := flags.Bool("test", false, "explicitly permit TEST key or artifacts")
	publicPreview := flags.Bool("public-preview", false, "explicitly build or verify an unsigned public preview, separate from production")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *test && *publicPreview {
		return errors.New("test and public-preview are mutually exclusive")
	}
	if *dir == "" {
		return errors.New("--dir is required")
	}
	if args[0] == "keygen" {
		if *publicPreview && *root != "" {
			return generatePreviewKey(*dir, *root)
		}
		if !*test || *root == "" {
			return errors.New("key generation requires --test and --root; production keys must be supplied externally")
		}
		return generateTestKey(*dir, *root)
	}
	if !stable.MatchString(packaging.Version()) {
		return errors.New("invalid packaging/VERSION")
	}
	if !repositoryPattern.MatchString(*repo) {
		return errors.New("real GitHub owner/repository required")
	}
	pub, err := parsePublic(*pubText)
	if err != nil {
		return err
	}
	if args[0] == "verify" {
		if *publicPreview {
			return verifyChannel(*dir, *repo, pub, "public-preview")
		}
		return verify(*dir, *repo, pub, *test)
	}
	if args[0] != "bundle" {
		return errors.New("unsupported command")
	}
	if *exe == "" {
		return errors.New("--exe required")
	}
	key, err := loadKey(*keyFile, *root)
	if err != nil {
		return err
	}
	if !bytes.Equal(key.Public().(ed25519.PublicKey), pub) {
		return errors.New("private/public key mismatch")
	}
	notes, err := releaseNotes(*notesFile, *root, *test)
	if err != nil {
		return err
	}
	if *publicPreview {
		if *keyFile == "" {
			return errors.New("public preview requires an explicit external key file")
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(*keyFile), "TEST-ONLY")); err == nil {
			return errors.New("test keys cannot sign a public preview")
		}
		return bundleChannel(*dir, *exe, *repo, key, "public-preview", notes)
	}
	return bundle(*dir, *exe, *repo, key, *test, notes)
}

func releaseNotes(filename, root string, test bool) (string, error) {
	if filename == "" && root != "" {
		filename = filepath.Join(root, "docs", "releases", packaging.Version()+".md")
	}
	data, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) && test {
		return "# ToolNest " + packaging.Version() + "\n\n- 此版本仅供更新流程测试，请勿用于正式工作环境。\n", nil
	}
	if err != nil {
		return "", fmt.Errorf("version-specific release notes required: %w", err)
	}
	notes := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	if err := validateNotes(notes); err != nil {
		return "", err
	}
	return notes, nil
}

func validateNotes(notes string) error {
	if len(notes) > 64<<10 || !utf8.ValidString(notes) || !strings.HasPrefix(notes, "# ToolNest "+packaging.Version()+"\n") || !strings.Contains(notes, "\n- ") {
		return errors.New("release notes must contain the matching version heading and user-visible changes")
	}
	if strings.Contains(notes, "TODO") || strings.Contains(notes, "待填写") || strings.Contains(notes, "See signed release-index.json") {
		return errors.New("placeholder release notes are forbidden")
	}
	return nil
}

func within(path, root string) bool {
	p, e1 := filepath.Abs(path)
	r, e2 := filepath.Abs(root)
	if e1 != nil || e2 != nil {
		return true
	}
	relative, err := filepath.Rel(r, p)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
func generateTestKey(dir, root string) error {
	return generateKey(dir, root, "TEST-ONLY", "NOT FOR PRODUCTION\n")
}
func generatePreviewKey(dir, root string) error {
	return generateKey(dir, root, "PUBLIC-PREVIEW", "ToolNest public preview signing identity; keep private key external and backed up.\n")
}
func generateKey(dir, root, marker, message string) error {
	if within(dir, root) {
		return errors.New("keys must live outside the repository")
	}
	// 独立新目录避免覆盖任何已存在凭据；测试标记不可用于生产。
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, marker), []byte(message), 0600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "mygo-update.key"), []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "mygo-update.pub"), []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0644)
}
func parsePublic(text string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("trusted public key must be base64 Ed25519 (32 bytes)")
	}
	return ed25519.PublicKey(b), nil
}
func loadKey(path, root string) (ed25519.PrivateKey, error) {
	text := os.Getenv("MYGO_UPDATER_PRIVATE_KEY")
	if path != "" {
		if root == "" || within(path, root) {
			return nil, errors.New("private key path must be outside the repository")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		text = string(b)
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, errors.New("external MyGo private key required (base64 64-byte Ed25519)")
	}
	key := ed25519.PrivateKey(b)
	if !bytes.Equal(ed25519.NewKeyFromSeed(key.Seed()), key) {
		return nil, errors.New("inconsistent Ed25519 private key")
	}
	return key, nil
}

// 与固定 MyGo cmd/mygo/updates.go writeSigned 及 internal/update.Sign 完全一致：
// 签 SHA-256 原始摘要（不是 hex 文本），输出标准 base64，绝不自创签名封装。
func sign(key ed25519.PrivateKey, data []byte) string {
	sum := sha256.Sum256(data)
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, sum[:]))
}
func validSignature(pub ed25519.PublicKey, data []byte, text string) bool {
	sig, err := base64.StdEncoding.DecodeString(text)
	sum := sha256.Sum256(data)
	return err == nil && ed25519.Verify(pub, sum[:], sig)
}
func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}
func archive(dir, exe string) (string, error) {
	info, err := os.Stat(exe)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 2 {
		return "", errors.New("regular nonempty executable required")
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		return "", err
	}
	if !bytes.HasPrefix(b, []byte("MZ")) {
		return "", errors.New("Windows PE executable required")
	}
	name := "ToolNest-" + packaging.Version() + "-windows-amd64.tar.gz"
	out, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	d, _ := json.Marshal(descriptor{packaging.Version(), "windows-amd64", "ToolNest.exe"})
	for _, entry := range []struct {
		name string
		data []byte
		mode int64
	}{{descriptorName, d, 0644}, {"ToolNest.exe", b, 0755}} {
		if err = tw.WriteHeader(&tar.Header{Name: entry.name, Size: int64(len(entry.data)), Mode: entry.mode, Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0)}); err != nil {
			break
		}
		_, err = tw.Write(entry.data)
		if err != nil {
			break
		}
	}
	for _, close := range []func() error{tw.Close, gz.Close, out.Close} {
		if e := close(); err == nil {
			err = e
		}
	}
	return name, err
}
func requiredNames() []string {
	v := packaging.Version()
	return []string{"ToolNest-" + v + "-windows-amd64.tar.gz", "ToolNest-Setup-" + v + "-windows-amd64.exe", "release-identity.json", "update-windows-amd64.json", "update-public-key.txt"}
}
func bundle(dir, exe, repo string, key ed25519.PrivateKey, test bool, notes string) error {
	channel := "production"
	if test {
		channel = "test"
	}
	return bundleChannel(dir, exe, repo, key, channel, notes)
}
func bundleChannel(dir, exe, repo string, key ed25519.PrivateKey, channel, notes string) error {
	if err := validateNotes(notes); err != nil {
		return err
	}
	name, err := archive(dir, exe)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	address := "https://github.com/" + repo + "/releases/download/v" + packaging.Version() + "/" + name
	if err = writeJSON(filepath.Join(dir, "update-windows-amd64.json"), manifest{Version: packaging.Version(), Notes: notes, URL: address, Size: int64(len(b)), Signature: sign(key, b)}); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "update-public-key.txt"), []byte(base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))+"\n"), 0644); err != nil {
		return err
	}
	inv := inventory{Version: packaging.Version(), Channel: channel, Repository: repo, MyGoCommit: mygoCommit, GoVersion: goVersion}
	names := requiredNames()
	sort.Strings(names)
	var sums strings.Builder
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		a := artifact{name, int64(len(data)), hex.EncodeToString(sum[:]), sign(key, data)}
		inv.Files = append(inv.Files, a)
		fmt.Fprintf(&sums, "%s  %s\n", a.SHA256, name)
	}
	if err = writeJSON(filepath.Join(dir, "release-index.json"), inv); err != nil {
		return err
	}
	index, err := os.ReadFile(filepath.Join(dir, "release-index.json"))
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "release-index.json.sig"), []byte(sign(key, index)+"\n"), 0644); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0644); err != nil {
		return err
	}
	return verifyChannel(dir, repo, key.Public().(ed25519.PublicKey), channel)
}
func decode(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}
func verify(dir, repo string, pub ed25519.PublicKey, allowTest bool) error {
	channel := "production"
	if allowTest {
		channel = "test"
	}
	return verifyChannel(dir, repo, pub, channel)
}
func verifyChannel(dir, repo string, pub ed25519.PublicKey, allowedChannel string) error {
	read := func(name string) ([]byte, error) {
		path := filepath.Join(dir, name)
		s, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !s.Mode().IsRegular() || s.Size() < 1 || s.Size() > 1<<30 {
			return nil, errors.New("artifact must be regular and bounded")
		}
		return os.ReadFile(path)
	}
	index, err := read("release-index.json")
	if err != nil {
		return err
	}
	signature, err := read("release-index.json.sig")
	if err != nil {
		return err
	}
	if !validSignature(pub, index, strings.TrimSpace(string(signature))) {
		return errors.New("release inventory signature invalid")
	}
	var inv inventory
	if err = decode(index, &inv); err != nil {
		return err
	}
	if inv.Version != packaging.Version() || inv.Repository != repo || inv.MyGoCommit != mygoCommit || inv.GoVersion != goVersion {
		return errors.New("release identity mismatch")
	}
	if inv.Channel != "production" && inv.Channel != allowedChannel {
		return errors.New("test artifacts forbidden for production")
	}
	expected := requiredNames()
	sort.Strings(expected)
	if len(inv.Files) != len(expected) {
		return errors.New("incomplete artifact inventory")
	}
	var sums strings.Builder
	for i, a := range inv.Files {
		if a.Name != expected[i] {
			return errors.New("duplicate, unexpected or missing artifact")
		}
		data, err := read(a.Name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if int64(len(data)) != a.Size || hex.EncodeToString(sum[:]) != a.SHA256 || !validSignature(pub, data, a.Signature) {
			return fmt.Errorf("artifact hash, size or signature invalid: %s", a.Name)
		}
		fmt.Fprintf(&sums, "%s  %s\n", a.SHA256, a.Name)
	}
	sumFile, err := read("SHA256SUMS")
	if err != nil {
		return err
	}
	if string(sumFile) != sums.String() {
		return errors.New("SHA256SUMS mismatch")
	}
	public, err := read("update-public-key.txt")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(public)) != base64.StdEncoding.EncodeToString(pub) {
		return errors.New("artifact public key differs from trusted key")
	}
	mdata, err := read("update-windows-amd64.json")
	if err != nil {
		return err
	}
	var m manifest
	if err = decode(mdata, &m); err != nil {
		return err
	}
	if inv.Channel != "test" {
		if err := validateNotes(m.Notes); err != nil {
			return err
		}
	}
	archiveName := "ToolNest-" + packaging.Version() + "-windows-amd64.tar.gz"
	data, err := read(archiveName)
	if err != nil {
		return err
	}
	if m.Version != inv.Version || m.Size != int64(len(data)) || m.URL != "https://github.com/"+repo+"/releases/download/v"+inv.Version+"/"+archiveName || !validSignature(pub, data, m.Signature) {
		return errors.New("update manifest identity/signature mismatch")
	}
	if err = verifyArchive(data, inv.Version); err != nil {
		return err
	}
	identity, err := read("release-identity.json")
	if err != nil {
		return err
	}
	var info packaging.Identity
	if err = decode(identity, &info); err != nil {
		return err
	}
	if info.Version != inv.Version || info.Repository != inv.Repository || info.Channel != inv.Channel || info.PublicKey != base64.StdEncoding.EncodeToString(pub) || info.RuntimeProtocol != 1 {
		return errors.New("application build identity or main lifecycle binding missing")
	}
	allowed := map[string]bool{"release-index.json": true, "release-index.json.sig": true, "SHA256SUMS": true}
	for _, name := range expected {
		allowed[name] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return fmt.Errorf("unexpected publish artifact: %s", entry.Name())
		}
	}
	return nil
}
func verifyArchive(data []byte, version string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()
	gz.Multistream(false)
	reader := tar.NewReader(gz)
	seen := map[string]bool{}
	for {
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || seen[h.Name] || h.Size < 1 || h.Size > 1<<30 {
			return errors.New("unsafe archive entry")
		}
		seen[h.Name] = true
		switch h.Name {
		case descriptorName:
			if h.Size > 4096 {
				return errors.New("oversized descriptor")
			}
			b, err := io.ReadAll(reader)
			if err != nil {
				return err
			}
			var d descriptor
			if err = decode(b, &d); err != nil {
				return err
			}
			if d.Version != version || d.Target != "windows-amd64" || d.Executable != "ToolNest.exe" {
				return errors.New("descriptor mismatch")
			}
		case "ToolNest.exe":
			var mz [2]byte
			if _, err = io.ReadFull(reader, mz[:]); err != nil || string(mz[:]) != "MZ" {
				return errors.New("invalid executable")
			}
		default:
			return errors.New("unexpected archive entry")
		}
	}
	if len(seen) != 2 {
		return errors.New("incomplete update archive")
	}
	_, err = io.Copy(io.Discard, gz)
	return err
}
