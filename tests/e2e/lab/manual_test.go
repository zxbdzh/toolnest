package lab

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestManualReleaseVersionsNotesAndSourceFailure(t *testing.T) {
	archive, err := Archive([]byte("MZ-manual"), "0.1.4")
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	environment, err := StartSignedRelease(archive, base64.StdEncoding.EncodeToString(ed25519.Sign(private, digest[:])), base64.StdEncoding.EncodeToString(public),
		ReleaseOptions{Version: "0.1.4", CurrentVersion: "0.1.3", Notes: "中文版本说明"})
	if err != nil {
		t.Fatal(err)
	}
	defer environment.Close()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	for endpoint, expected := range map[string]string{"/manifest": "0.1.4", "/current": "0.1.3"} {
		response, err := client.Get(environment.Feed + endpoint)
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct{ Version string }
		err = json.NewDecoder(response.Body).Decode(&manifest)
		response.Body.Close()
		if err != nil || manifest.Version != expected {
			t.Fatalf("manual version mismatch: %+v, %v", manifest, err)
		}
	}
	response, err := client.Get(environment.Feed + "/notes")
	if err != nil {
		t.Fatal(err)
	}
	notes, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(notes) != "中文版本说明" {
		t.Fatal("manual notes were replaced")
	}
	if err := environment.SetMode("source-not-found"); err != nil {
		t.Fatal(err)
	}
	response, err = client.Get(environment.Feed + "/manifest")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound || environment.PayloadRequests.Load() != 0 {
		t.Fatal("source failure reached the download")
	}
}
