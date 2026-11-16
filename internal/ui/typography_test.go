package ui

import (
	"testing"

	native "github.com/egoist/mygo/ui"

	"toolnest/internal/app"
)

func TestTextSizeSettingRendersThreeScales(t *testing.T) {
	for _, scale := range []struct {
		name string
		size float32
	}{
		{"compact", 13},
		{"standard", 14},
		{"comfortable", 16},
	} {
		actions := fixture()
		actions.state.Settings.TextSize = scale.name
		controller := New(actions)
		controller.ShowSettings()
		controller.synchronize()
		controller.systemDark = false
		tester := native.NewTester(controller.render, 1180, 780)
		_ = tester
		if got := controller.renderTheme().FontSize; got != scale.size {
			t.Fatalf("%s text size renders %v, want %v", scale.name, got, scale.size)
		}
		if controller.draft.TextSize != scale.name {
			t.Fatalf("%s text size draft became %q", scale.name, controller.draft.TextSize)
		}
		for _, label := range []string{"主题", "字号", "小", "标准", "大"} {
			if !tester.HasText(label) {
				t.Fatalf("%s settings missing %q", scale.name, label)
			}
		}
	}
}

func TestOldConfigUsesStandardTextSize(t *testing.T) {
	settings := app.Settings{}
	if err := validateDraft(settings); err != nil {
		t.Fatalf("old settings without text size must stay valid: %v", err)
	}
	theme := refinedTheme(native.LightTheme(), settings)
	if theme.FontSize != 14 {
		t.Fatalf("old settings render %v, want standard 14", theme.FontSize)
	}
}
