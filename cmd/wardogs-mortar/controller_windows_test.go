//go:build windows && amd64

package main

import (
	"testing"

	"wardogs-mortar/internal/config"
	"wardogs-mortar/internal/win32"
)

func TestValidateSettings(t *testing.T) {
	valid := UISettings{
		ReferenceWidth:  3440,
		ReferenceHeight: 1440,
		ChatInput:       config.Rect{X: 35, Y: 250, W: 510, H: 75},
		MapRegion:       config.Rect{X: 1200, Y: 230, W: 1050, H: 980},
		OverlayXPercent: 100,
		OverlayYPercent: 0,
	}
	if err := validateSettings(valid); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}

	invalid := valid
	invalid.ChatInput.W = 4000
	if err := validateSettings(invalid); err == nil {
		t.Fatal("out-of-bounds settings were accepted")
	}

	invalid = valid
	invalid.OverlayYPercent = 101
	if err := validateSettings(invalid); err == nil {
		t.Fatal("out-of-bounds overlay position was accepted")
	}
}

func TestRescaleConfigRectToMonitor(t *testing.T) {
	got := rescaleConfigRect(config.Rect{X: 35, Y: 250, W: 510, H: 75}, 3440, 1440, 1920, 1080)
	want := config.Rect{X: 19, Y: 187, W: 284, H: 56}
	if got != want {
		t.Fatalf("scaled rect=%+v, want %+v", got, want)
	}
}

func TestCalibrationResizeStaysInsideMonitor(t *testing.T) {
	start := win32.Rect{Left: 100, Top: 120, Right: 500, Bottom: 420}
	got := resizeCalibrationRect(start, -200, -200, calibrationHandleNW, 1920, 1080)
	want := win32.Rect{Left: 0, Top: 0, Right: 500, Bottom: 420}
	if got != want {
		t.Fatalf("resized rect=%+v, want %+v", got, want)
	}

	got = moveCalibrationRect(start, 2000, 900, 1920, 1080)
	want = win32.Rect{Left: 1520, Top: 780, Right: 1920, Bottom: 1080}
	if got != want {
		t.Fatalf("moved rect=%+v, want %+v", got, want)
	}
}
