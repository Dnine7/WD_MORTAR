package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

type Config struct {
	ReferenceWidth  int    `json:"referenceWidth"`
	ReferenceHeight int    `json:"referenceHeight"`
	ChatInput       Rect   `json:"chatInput"`
	MapRegion       Rect   `json:"mapRegion"`
	BoundProcess    string `json:"boundProcess,omitempty"`
	OverlayVisible  bool   `json:"overlayVisible"`
	OverlayXPercent int    `json:"overlayXPercent"`
	OverlayYPercent int    `json:"overlayYPercent"`
}

func Default() Config {
	return Config{
		ReferenceWidth:  3440,
		ReferenceHeight: 1440,
		// Defaults are based on the supplied 3440x1440 screenshots. The
		// tray menu can later be extended with region calibration.
		// Keep this crop tight around the active input row so older chat
		// messages containing coordinates cannot win the source priority.
		ChatInput:      Rect{X: 35, Y: 250, W: 510, H: 75},
		MapRegion:      Rect{X: 1200, Y: 230, W: 1050, H: 980},
		OverlayVisible: true,
		// Percentages position the overlay within the monitor's available
		// width and height, so the same setting works at any resolution.
		OverlayXPercent: 100,
		OverlayYPercent: 0,
	}
}

func Path() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		fallback, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		base = fallback
	}
	return filepath.Join(base, "WardogsMortar", "config.json"), nil
}

func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}

	result := Default()
	if err := json.Unmarshal(data, &result); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	return result, nil
}

func Save(value Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
