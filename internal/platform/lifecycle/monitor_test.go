package lifecycle

import (
	"errors"
	"slices"
	"testing"
)

func TestProbeTrayOnlyNotifiesOnLossTransition(t *testing.T) {
	c, events := controllerFixture(t)
	tray := &fakeTray{available: true}
	c.Start(AutostartLaunch, true, func(func(TrayAction)) (Tray, error) { return tray, nil })
	*events = nil
	tray.available = false
	c.ProbeTray()
	c.ProbeTray()
	if !slices.Equal(*events, []string{"show", "error"}) {
		t.Fatal(*events)
	}
	tray.available = true
	c.ProbeTray()
	tray.available = false
	c.ProbeTray()
	if len(*events) != 4 {
		t.Fatal("availability recovery did not reset transition", *events)
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
