package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"toolnest/internal/network"
)

const DescriptorName = ".toolnest-release.json"

// Descriptor is INSIDE the signed tar.gz, preventing manifest version replay.
type Descriptor struct {
	Version    string `json:"version"`
	Target     string `json:"target"`
	Executable string `json:"executable"`
}

type Prepared struct {
	Version    string `json:"version"`
	Archive    string `json:"archive"`
	Executable string `json:"executable"`
	Signature  string `json:"signature"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	WorkDir    string `json:"workDir"`
}

func (manager *Manager) Prepare(ctx context.Context) (prepared *Prepared, err error) {
	if ctx, err = manager.beginNetwork(ctx, Downloading); err != nil {
		return nil, err
	}
	defer func() { manager.end(err) }()
	manager.mu.Lock()
	release := manager.release
	existing := manager.prepared
	manualRetry := manager.manualRetry
	manager.mu.Unlock()
	if existing != nil {
		if err = manager.validatePrepared(existing); err != nil {
			manager.mu.Lock()
			manager.prepared = nil
			manager.mu.Unlock()
			return nil, err
		}
		copy := *existing
		return &copy, nil
	}
	if release == nil {
		return nil, ErrNotReady
	}
	blocked, blockedErr := manager.blockedVersion()
	if blockedErr != nil {
		return nil, blockedErr
	}
	if blocked == release.Version && !manualRetry {
		return nil, ErrBlocked
	}
	if err = os.MkdirAll(manager.config.StateDir, 0700); err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp(manager.config.StateDir, "prepare-")
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(work)
		}
	}()
	client := manager.config.Network.Snapshot()
	archive := filepath.Join(work, "update.tar.gz")
	for _, delta := range release.Deltas {
		if delta.From != manager.config.CurrentVersion {
			continue
		}
		if manager.config.ApplyDelta == nil {
			manager.set(func(status *Status) {
				status.FallbackReason = "compatible MyGo delta adapter unavailable; using complete archive"
			})
			break
		}
		manager.set(func(status *Status) { status.UsingDelta = true })
		deltaPath := filepath.Join(work, "update.delta")
		deltaErr := manager.download(ctx, client, delta.URL, delta.Size, delta.Signature, deltaPath)
		if deltaErr == nil {
			deltaErr = manager.config.ApplyDelta(ctx, deltaPath, filepath.Join(manager.config.InstallDir, manager.config.Executable), archive, delta.From, release.Version)
		}
		if deltaErr == nil {
			deltaErr = verifyFile(archive, release.Size, release.Signature, manager.key, manager.config.MaxDownload)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if deltaErr == nil {
			break
		}
		// 差分失败只回退到同版本完整签名包；不绕过验签或改走直连。
		_ = os.Remove(archive)
		manager.set(func(status *Status) {
			status.UsingDelta = false
			status.FallbackReason = "delta could not produce the signed archive; using complete archive"
		})
		break
	}
	if _, statErr := os.Stat(archive); statErr != nil {
		if err = manager.download(ctx, client, release.URL, release.Size, release.Signature, archive); err != nil {
			return nil, err
		}
	}
	manager.set(func(status *Status) { status.Phase = Verifying; status.NetworkPhase = Verifying })
	if err = verifyFile(archive, release.Size, release.Signature, manager.key, manager.config.MaxDownload); err != nil {
		return nil, err
	}
	payload := filepath.Join(work, manager.config.Executable)
	if err = extractExecutable(ctx, archive, payload, release.Version, manager.config.Executable, manager.config.MaxExecutable); err != nil {
		return nil, err
	}
	digest, err := fileDigest(payload, manager.config.MaxExecutable)
	if err != nil {
		return nil, err
	}
	prepared = &Prepared{Version: release.Version, Archive: archive, Executable: payload, Signature: release.Signature, Size: release.Size, SHA256: digest, WorkDir: work}
	if err = writeJSON(filepath.Join(manager.config.StateDir, "prepared.json"), prepared); err != nil {
		return nil, err
	}
	if manualRetry {
		if err = os.Remove(filepath.Join(manager.config.StateDir, "blocked.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	manager.mu.Lock()
	manager.prepared = prepared
	manager.manualRetry = false
	manager.presentation.Version, manager.presentation.Notes = release.Version, release.Notes
	manager.presentation.PreparedAt = time.Now()
	err = manager.savePresentationLocked()
	manager.mu.Unlock()
	keep = true
	if err != nil {
		return nil, err
	}
	copy := *prepared
	return &copy, nil
}

func (manager *Manager) download(ctx context.Context, client *network.Client, endpoint string, size int64, signature, destination string) error {
	if size <= 0 || size > manager.config.MaxDownload {
		return ErrSize
	}
	decoded, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || len(decoded) != ed25519.SignatureSize {
		return ErrSignature
	}
	response, err := open(ctx, client, endpoint, manager.config.Source.allow)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.ContentLength >= 0 && response.ContentLength != size {
		return ErrSize
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	started := time.Now()
	reader := &progressReader{reader: io.LimitReader(response.Body, size+1), progress: func(downloaded int64) {
		manager.set(func(status *Status) {
			status.Phase = Downloading
			status.Downloaded = downloaded
			status.Total = size
			status.BytesPerSecond = float64(downloaded) / time.Since(started).Seconds()
		})
	}}
	count, copyErr := io.Copy(file, reader)
	syncErr := file.Sync()
	closeErr := file.Close()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if copyErr != nil {
		return network.ErrNetwork
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if count != size {
		return ErrSize
	}
	return verifyFile(destination, size, signature, manager.key, manager.config.MaxDownload)
}

type progressReader struct {
	reader     io.Reader
	downloaded int64
	progress   func(int64)
}

func (reader *progressReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	reader.downloaded += int64(count)
	reader.progress(reader.downloaded)
	return count, err
}

func verifyFile(path string, size int64, signature string, key ed25519.PublicKey, limit int64) error {
	if size <= 0 || size > limit {
		return ErrSize
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return ErrSize
	}
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, size+1))
	if err != nil {
		return err
	}
	if count != size {
		return ErrSize
	}
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || !ed25519.Verify(key, hash.Sum(nil), sig) {
		return ErrSignature
	}
	return nil
}
func fileDigest(path string, limit int64) (string, error) {
	if info, err := os.Lstat(path); err != nil {
		return "", err
	} else if !info.Mode().IsRegular() {
		return "", ErrArchive
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > limit {
		return "", ErrSize
	}
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, limit+1))
	if err != nil {
		return "", err
	}
	if count > limit {
		return "", ErrSize
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func extractExecutable(ctx context.Context, archive, destination, version, executable string, limit int64) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return ErrArchive
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	foundExecutable, foundDescriptor := false, false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ErrArchive
		}
		// 首版明确采用单 exe 策略；多文件、链接、ADS、大小写别名均拒绝，避免半升级。
		if header.Typeflag != tar.TypeReg || header.Name != executable && header.Name != DescriptorName {
			return ErrArchive
		}
		if header.Name == DescriptorName {
			if foundDescriptor || header.Size < 1 || header.Size > 4096 {
				return ErrArchive
			}
			foundDescriptor = true
			var descriptor Descriptor
			data, err := io.ReadAll(io.LimitReader(reader, 4097))
			if err != nil || json.Unmarshal(data, &descriptor) != nil {
				return ErrArchive
			}
			if descriptor.Version != version || descriptor.Target != "windows-amd64" || descriptor.Executable != executable {
				return ErrVersion
			}
			continue
		}
		if foundExecutable || header.Size < 1 || header.Size > limit {
			return ErrSize
		}
		foundExecutable = true
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			return err
		}
		count, copyErr := io.Copy(output, io.LimitReader(reader, limit+1))
		syncErr := output.Sync()
		closeErr := output.Close()
		if copyErr != nil {
			return ErrArchive
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		if count != header.Size {
			return ErrSize
		}
	}
	if !foundExecutable || !foundDescriptor {
		return ErrArchive
	}
	// tar 结束不等于 gzip CRC 已验证；继续读到压缩流尾，拒绝损坏尾部。
	if count, err := io.Copy(io.Discard, io.LimitReader(compressed, 1025)); err != nil || count > 1024 {
		return ErrArchive
	}
	return nil
}

func (manager *Manager) loadPrepared() error {
	path := filepath.Join(manager.config.StateDir, "prepared.json")
	var prepared Prepared
	if err := readJSON(path, &prepared); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := manager.validatePrepared(&prepared); err != nil {
		return err
	}
	comparison, err := compareVersions(prepared.Version, manager.config.CurrentVersion)
	if err != nil {
		return err
	}
	if comparison <= 0 {
		return nil
	}
	blocked, err := manager.blockedVersion()
	if err != nil {
		return err
	}
	if blocked == prepared.Version {
		manager.status.Phase = Blocked
		manager.status.AvailableVersion = prepared.Version
		manager.status.Failure = ErrBlocked.Error()
		return nil
	}
	manager.prepared = &prepared
	manager.readyLocked()
	return nil
}
func (manager *Manager) validatePrepared(prepared *Prepared) error {
	if prepared == nil {
		return ErrNotReady
	}
	if _, err := stableVersion(prepared.Version); err != nil {
		return err
	}
	if filepath.Dir(prepared.WorkDir) != filepath.Clean(manager.config.StateDir) || !strings.HasPrefix(filepath.Base(prepared.WorkDir), "prepare-") || prepared.Archive != filepath.Join(prepared.WorkDir, "update.tar.gz") || prepared.Executable != filepath.Join(prepared.WorkDir, manager.config.Executable) {
		return ErrArchive
	}
	for _, path := range []string{prepared.WorkDir, prepared.Archive, prepared.Executable} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrArchive
		}
	}
	if err := verifyFile(prepared.Archive, prepared.Size, prepared.Signature, manager.key, manager.config.MaxDownload); err != nil {
		return err
	}
	digest, err := fileDigest(prepared.Executable, manager.config.MaxExecutable)
	if err != nil {
		return err
	}
	if digest != prepared.SHA256 {
		return ErrSignature
	}
	return nil
}
