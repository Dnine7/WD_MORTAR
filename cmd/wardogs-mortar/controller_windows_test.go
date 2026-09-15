//go:build windows && amd64

package main

import (
	"testing"

	"wardogs-mortar/internal/config"
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
