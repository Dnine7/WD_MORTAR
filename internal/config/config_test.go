package config

import (
	"path/filepath"
	"testing"
)

func TestPathUsesLocalAppData(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "WardogsMortar", "config.json")
	if path != want {
		t.Fatalf("path=%q, want %q", path, want)
	}
}

func TestDefaultRegionsMatchReferenceResolution(t *testing.T) {
	value := Default()
	if value.ReferenceWidth != 3440 || value.ReferenceHeight != 1440 {
		t.Fatalf("reference=%dx%d", value.ReferenceWidth, value.ReferenceHeight)
	}
	for name, rect := range map[string]Rect{"chat": value.ChatInput, "map": value.MapRegion} {
		if rect.W <= 0 || rect.H <= 0 || rect.X < 0 || rect.Y < 0 || rect.X+rect.W > value.ReferenceWidth || rect.Y+rect.H > value.ReferenceHeight {
			t.Fatalf("%s region outside reference image: %+v", name, rect)
		}
	}
	if value.OverlayXPercent != 100 || value.OverlayYPercent != 0 {
		t.Fatalf("default overlay position=%d,%d", value.OverlayXPercent, value.OverlayYPercent)
	}
}
