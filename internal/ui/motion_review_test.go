package ui

import (
	"encoding/json"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	native "github.com/egoist/mygo/ui"
)

func TestMotionReview(t *testing.T) {
	directory := os.Getenv("TOOLNEST_MOTION_REVIEW_DIR")
	if directory == "" {
		t.Skip("set TOOLNEST_MOTION_REVIEW_DIR to export sampled animation frames")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	var observations []map[string]any
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		controller := New(fixture())
		tester := native.NewTesterWith(controller.render, 1180, 780, native.TesterOptions{
			Dark: dark, Preferences: native.Preferences{ReduceTransparency: true},
		})
		baseline := tester.Image().RGBAAt(1156, 700)
		strip := image.NewRGBA(image.Rect(0, 0, 4*184, 2*250))
		labels := []string{"快照", "概览", "自动备份", "活动记录"}
		for index := range 8 {
			if index%2 == 0 {
				if err := tester.Click(labels[index/2]); err != nil {
					t.Fatal(err)
				}
			}
			started := time.Now()
			tester.Advance(40 * time.Millisecond)
			elapsed := time.Since(started)
			frame := tester.Image()
			// 页面内容可以切换，但底层玻璃的安静区域不能突然变白、变黑或透明。
			if pixel := frame.RGBAAt(1156, 700); pixel != baseline || pixel.A != 255 {
				t.Fatalf("workspace material flashed during %s navigation frame %d: %v -> %v", name, index, baseline, pixel)
			}
			crop := image.Rect((index%4)*184, (index/4)*250, (index%4+1)*184, (index/4+1)*250)
			draw.Draw(strip, crop, frame, image.Pt(12, 84), draw.Src)
			observations = append(observations, map[string]any{
				"theme": name, "sample": index, "advanceMs": 40, "cpuHeadlessStepMs": float64(elapsed.Microseconds()) / 1000,
			})
		}
		file, err := os.Create(filepath.Join(directory, "navigation-"+name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		encodeErr := png.Encode(file, strip)
		closeErr := file.Close()
		if encodeErr != nil || closeErr != nil {
			t.Fatal(encodeErr, closeErr)
		}
	}
	data, err := json.MarshalIndent(map[string]any{
		"scope": "CPU headless interrupted-navigation samples, not native GPU display FPS", "samples": observations,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "motion-samples.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
