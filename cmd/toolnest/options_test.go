package toolnest

import (
	"bytes"
	"image/png"
	"io"
	"path/filepath"
	"reflect"
	"testing"
)

const isolatedID = "0123456789abcdef0123456789abcdef"

func TestOptionsRequirePairedIsolation(t *testing.T) {
	directory := t.TempDir()
	for _, arguments := range [][]string{
		{"--data-dir", directory}, {"--test-root", isolatedID},
		{"--data-dir", directory, "--test-root", "Software\\Environment"},
		{"--data-dir", directory, "--test-root", "ABCDEF0123456789ABCDEF0123456789"},
		{"--data-dir", "relative", "--test-root", isolatedID},
		{"--smoke"}, {"--resume-tray"}, {"--test-update-source", "http://127.0.0.1:1234/manifest"},
		{"--registry-key", "Environment"}, {"unrecognized"},
	} {
		if _, err := parseOptions(arguments, io.Discard); err == nil {
			t.Errorf("unsafe arguments accepted: %v", arguments)
		}
	}
	if _, err := parseOptions([]string{"--data-dir", directory, "--test-root", isolatedID}, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateHelperRoundTripPreservesIsolation(t *testing.T) {
	directory := t.TempDir()
	key := filepath.Join(t.TempDir(), "public-key.txt")
	args := []string{"--data-dir", directory, "--test-root", isolatedID, "--test-update-source", "http://127.0.0.1:4312/update.json", "--test-update-key-file", key}
	parsed, err := parseOptions(args, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed.runtimeArguments(), args) {
		t.Fatalf("runtime lost isolation: %v", parsed.runtimeArguments())
	}
	token := isolatedID + isolatedID
	helper := append(parsed.runtimeArguments(), "--toolnest-update-target", filepath.Join(directory, "ToolNest.exe"), "--toolnest-update-helper", filepath.Join(directory, "helper.json"), token)
	request, err := parseOptions(helper, io.Discard)
	if err != nil || request.helperToken != token || request.testRoot != isolatedID {
		t.Fatalf("helper parse: %+v %v", request, err)
	}
	confirmation := append(parsed.runtimeArguments(), "--resume-tray", "--toolnest-update-confirm", token)
	confirmed, err := parseOptions(confirmation, io.Discard)
	if err != nil || !confirmed.resumeTray || confirmed.confirmToken != token {
		t.Fatalf("confirmation parse: %+v %v", confirmed, err)
	}
}

func TestTrayIconsAreDistinctPNG(t *testing.T) {
	normal, failure := trayPNG(false), trayPNG(true)
	if bytes.Equal(normal, failure) {
		t.Fatal("error icon must differ from normal icon")
	}
	for _, data := range [][]byte{normal, failure} {
		decoded, err := png.Decode(bytes.NewReader(data))
		if err != nil || decoded.Bounds().Dx() != 32 || decoded.Bounds().Dy() != 32 {
			t.Fatalf("invalid tray PNG: %v", err)
		}
	}
}

func TestPathContainmentDoesNotUseStringPrefix(t *testing.T) {
	root := t.TempDir()
	if !within(root, filepath.Join(root, "child")) || !within(root, root) || within(root, root+"-sibling") {
		t.Fatal("unsafe directory overlap detection")
	}
}
