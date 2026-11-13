package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"toolnest/tests/e2e/lab"
)

func acceptanceRoot() string { return filepath.Join(os.TempDir(), "toolnest-acceptance") }

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("acceptance-runner", flag.ContinueOnError)
	directory := flags.String("out", "", "external private fixture configuration directory")
	executable := flags.String("exe", "", "0.1.1 executable; omit for explicitly synthetic infrastructure smoke")
	operation := flags.String("operation", "serve", "serve, keygen, or sign")
	keyFile := flags.String("key-file", "", "external TEST-ONLY Ed25519 private key, sign only")
	archiveFile := flags.String("archive", "", "pre-signed 0.1.1 archive to serve; no private key needed")
	signatureFile := flags.String("signature-file", "", "archive signature file")
	publicFile := flags.String("public-key-file", "", "trusted test public key file")
	version := flags.String("version", "0.1.1", "stable fixture archive version")
	currentVersion := flags.String("current-version", "0.1.0", "stable baseline version served by /current")
	duration := flags.Duration("duration", 10*time.Minute, "bounded fixture lifetime")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *directory == "" || *duration <= 0 || *duration > time.Hour {
		return fmt.Errorf("--out and a duration in (0,1h] required")
	}
	if !reserved(*directory) {
		return fmt.Errorf("--out must stay inside F:\\tmp\\toolnest-acceptance or guest C:\\ToolNestAcceptance\\work")
	}
	if !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(*version) {
		return fmt.Errorf("stable fixture version required")
	}
	if *operation == "keygen" {
		if _, err := os.Stat(*directory); !os.IsNotExist(err) {
			return fmt.Errorf("key output must be a new external directory")
		}
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		if err = os.Mkdir(*directory, 0700); err != nil {
			return err
		}
		for name, data := range map[string]string{"TEST-ONLY": "NOT FOR PRODUCTION\n", "update.key": base64.StdEncoding.EncodeToString(private), "update.pub": base64.StdEncoding.EncodeToString(public)} {
			if err = os.WriteFile(filepath.Join(*directory, name), []byte(data), 0600); err != nil {
				return err
			}
		}
		return nil
	}
	if *operation == "sign" {
		if *executable == "" || *keyFile == "" || !reserved(*keyFile) {
			return fmt.Errorf("sign requires --exe and --key-file inside the reserved acceptance directory")
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(*keyFile), "TEST-ONLY")); err != nil {
			return fmt.Errorf("TEST-ONLY key marker required")
		}
		keyText, err := os.ReadFile(*keyFile)
		if err != nil {
			return err
		}
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyText)))
		if err != nil || len(key) != ed25519.PrivateKeySize {
			return fmt.Errorf("invalid test key")
		}
		payload, err := os.ReadFile(*executable)
		if err != nil {
			return err
		}
		archive, err := lab.Archive(payload, *version)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(archive)
		signature := ed25519.Sign(ed25519.PrivateKey(key), digest[:])
		if err = os.MkdirAll(*directory, 0700); err != nil {
			return err
		}
		for name, data := range map[string][]byte{"update-" + *version + ".tar.gz": archive, "update-" + *version + ".sig": []byte(base64.StdEncoding.EncodeToString(signature)), "update.pub": []byte(base64.StdEncoding.EncodeToString(ed25519.PrivateKey(key).Public().(ed25519.PublicKey)))} {
			file, err := os.OpenFile(filepath.Join(*directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, writeErr := file.Write(data)
			closeErr := file.Close()
			if writeErr != nil {
				return writeErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	}
	if *operation != "serve" {
		return fmt.Errorf("unsupported operation")
	}
	payload := []byte("MZ-SYNTHETIC-NOT-AN-INSTALLED-APPLICATION")
	if *executable != "" {
		var err error
		payload, err = os.ReadFile(*executable)
		if err != nil {
			return err
		}
	}
	var environment *lab.Environment
	var err error
	if *archiveFile != "" {
		archive, readErr := os.ReadFile(*archiveFile)
		if readErr != nil {
			return readErr
		}
		signature, readErr := os.ReadFile(*signatureFile)
		if readErr != nil {
			return readErr
		}
		public, readErr := os.ReadFile(*publicFile)
		if readErr != nil {
			return readErr
		}
		environment, err = lab.StartSignedRelease(archive, strings.TrimSpace(string(signature)), strings.TrimSpace(string(public)),
			lab.ReleaseOptions{Version: *version, CurrentVersion: *currentVersion, Notes: *version + ": acceptance-only signed local update"})
	} else {
		environment, err = lab.Start(payload)
	}
	if err != nil {
		return err
	}
	defer environment.Close()
	if err = environment.WriteConfig(*directory); err != nil {
		return err
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ready": true, "synthetic": *executable == "" && *archiveFile == "", "feed": environment.Feed, "publicKey": environment.PublicKey, "httpProxy": environment.HTTPProxy, "socksProxy": environment.SOCKSProxy})
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	timer := time.NewTimer(*duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
	return nil
}

func reserved(path string) bool {
	for _, root := range []string{acceptanceRoot(), `C:\ToolNestAcceptance\work`} {
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			return true
		}
	}
	return false
}
