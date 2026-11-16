package lifecycle

import (
	"errors"
	"slices"
	"testing"
)

func TestProbeTrayPreservesBackgroundOnLossAndRecovery(t *testing.T) {
	c, events := controllerFixture(t)
	tray := &fakeTray{available: true}
	c.Start(AutostartLaunch, true, func(func(TrayAction)) (Tray, error) { return tray, nil })
	*events = nil
	tray.available = false
	c.ProbeTray()
	c.ProbeTray()
	if !slices.Equal(*events, []string{"error"}) {
		t.Fatal(*events)
	}
	tray.available = true
	c.ProbeTray()
	tray.available = false
	c.ProbeTray()
	if !slices.Equal(*events, []string{"error", "error"}) {
		t.Fatal("availability recovery did not reset transition", *events)
	}
}

func TestBackgroundStatusFailureNeverShowsWindow(t *testing.T) {
	for _, available := range []bool{false, true} {
		c, events := controllerFixture(t)
		tray := &fakeTray{available: true}
		c.Start(AutostartLaunch, true, func(func(TrayAction)) (Tray, error) { return tray, nil })
		*events = nil
		tray.available = available
		if available {
			tray.err = errors.New("status update failed")
		}
		c.SetStatus("自动备份完成", false)
		if !slices.Equal(*events, []string{"error"}) {
			t.Fatalf("background status changed visibility: %v", *events)
		}
		*events = nil
		c.SecondLaunch(ManualLaunch)
		if !slices.Equal(*events, []string{"show"}) {
			t.Fatal("manual launch must still recover the window", *events)
		}
	}
}
func TestTrayFactoryFailureNeverHides(t *testing.T) {
	c, events := controllerFixture(t)
	tray := &fakeTray{available: true}
	c.Start(AutostartLaunch, true, func(func(TrayAction)) (Tray, error) { return tray, errors.New("native tray rejected icon") })
	if !tray.closed || !slices.Equal(*events, []string{"show", "error"}) {
		t.Fatal(*events, tray.closed)
	}
	c.Hide()
	if !slices.Equal((*events)[2:], []string{"show", "error"}) {
		t.Fatal(*events)
	}
}
