package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"toolnest/internal/network"
)

func TestPreparedRecheckKeepsReadyAndAvoidsDownload(t *testing.T) {
	fixture := newFixture(t)
	prepared := fixture.prepare()
	if _, err := fixture.manager.Check(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if fixture.manager.Status().Phase != Ready {
		t.Fatal("checking the same release hid the ready update")
	}
	again, err := fixture.manager.Prepare(context.Background())
	if err != nil || again.SHA256 != prepared.SHA256 || fixture.downloadRequests.Load() != 1 {
		t.Fatalf("prepared release downloaded again: %v", err)
	}
	fixture.mode.Store(4)
	if _, err := fixture.manager.Check(context.Background(), false); err == nil {
		t.Fatal("expected HTTP failure")
	}
	if fixture.manager.Status().Phase != Ready || fixture.manager.Prepared() == nil {
		t.Fatal("failed check discarded the usable update")
	}
}

func TestUpdateHTTPStatusRetainsClassification(t *testing.T) {
	fixture := newFixture(t)
	fixture.mode.Store(4)
	_, err := fixture.manager.Check(context.Background(), true)
	var status *network.HTTPStatusError
	if !errors.As(err, &status) || status.StatusCode != 503 {
		t.Fatalf("HTTP status lost: %v", err)
	}
}

func TestPreparedMetadataSurvivesReopen(t *testing.T) {
	fixture := newFixture(t)
	fixture.prepare()
	before := fixture.manager.Status()
	reopened, err := New(fixture.manager.config)
	if err != nil {
		t.Fatal(err)
	}
	after := reopened.Status()
	if after.Phase != Ready || after.Notes != before.Notes || !after.LastCheck.Equal(before.LastCheck) {
		t.Fatalf("prepared metadata lost: %+v", after)
	}
}

func TestDeferredNoticePersistsButDoesNotHideCompletedUpdate(t *testing.T) {
	fixture := newFixture(t)
	fixture.prepare()
	if err := fixture.manager.DismissNotice("1.1.0"); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(fixture.manager.config)
	if err != nil || reopened.Status().DismissedVersion != "ready:1.1.0" || reopened.Prepared() == nil {
		t.Fatal("deferral lost the prepared update or notice preference", err)
	}
	if err := reopened.DismissNotice("9.9.9"); !errors.Is(err, ErrVersion) {
		t.Fatal("unrelated version dismissal accepted")
	}
}

func TestMissingPreparedFileCanBeDownloadedAgain(t *testing.T) {
	fixture := newFixture(t)
	prepared := fixture.prepare()
	if err := os.Remove(prepared.Executable); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Prepare(context.Background()); err == nil || fixture.manager.Prepared() != nil {
		t.Fatal("deleted staged payload remained ready")
	}
	if _, err := fixture.manager.Prepare(context.Background()); err != nil || fixture.downloadRequests.Load() != 2 {
		t.Fatal("missing payload could not be safely downloaded again", err)
	}
}

func TestHTTPFailureCodesRemainDistinct(t *testing.T) {
	for _, status := range []int{404, 407, 429, 503} {
		if code := failureCode(&network.HTTPStatusError{StatusCode: status}); code != fmt.Sprintf("http-%d", status) {
			t.Fatalf("lost HTTP classification: %s", code)
		}
	}
	if failureCode(ErrSignature) != "signature" || failureCode(network.ErrProxy) != "proxy" {
		t.Fatal("signature failure was confused with proxy failure")
	}
}
