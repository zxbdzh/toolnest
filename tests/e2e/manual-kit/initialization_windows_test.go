//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"toolnest/internal/app"
	"toolnest/internal/core"
)

type emptyRegistry struct{}

func (emptyRegistry) Read(context.Context) (core.State, error) { return core.State{}, nil }
func (emptyRegistry) Authorize(context.Context, []core.Scope) (func(), error) {
	return nil, errors.New("startup probe must not write PATH")
}
func (emptyRegistry) Write(context.Context, core.Scope, core.Value) error {
	return errors.New("startup probe must not write PATH")
}
func (emptyRegistry) Notify(context.Context) error {
	return errors.New("startup probe must not notify environment changes")
}

func TestManualFreshSetupIsHealthyBeforeFirstBackup(t *testing.T) {
	root := t.TempDir()
	paths := locations{data: root}
	if err := initializeData(paths); err != nil {
		t.Fatal(err)
	}
	controller, err := app.New(app.Options{Root: root, Version: "0.1.4", Registry: emptyRegistry{}})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Quit()
	view := controller.View()
	if view.Error != "" {
		t.Fatalf("fresh kit would reject updated startup confirmation: %s", view.Error)
	}
	if len(view.Snapshots) != 0 {
		t.Fatal("first launch must not invent a backup")
	}
	if view.Settings.AutoCheck || view.Settings.AutoDownload {
		t.Fatal("manual update needs an explicit first action")
	}
}

func TestManualSetupDoesNotRecreateUserRemovedSnapshotDirectory(t *testing.T) {
	root := t.TempDir()
	paths := locations{data: root}
	if err := initializeData(paths); err != nil {
		t.Fatal(err)
	}
	snapshots := filepath.Join(root, "snapshots")
	if _, err := os.Stat(snapshots); err == nil {
		if err := os.Remove(snapshots); err != nil {
			t.Fatal(err)
		}
	}
	if err := initializeData(paths); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snapshots); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("existing user configuration must not silently create a replacement store")
	}
}

func TestManualSetupPreservesPreviousConfigurationForAppRecovery(t *testing.T) {
	root := t.TempDir()
	previous := filepath.Join(root, "settings.json.previous")
	if err := os.WriteFile(previous, []byte("existing recovery copy"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := initializeData(locations{data: root}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("default configuration would shadow the existing recovery copy")
	}
}
