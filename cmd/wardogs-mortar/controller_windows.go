//go:build windows && amd64

package main

import (
	"fmt"

	"wardogs-mortar/internal/config"
	"wardogs-mortar/internal/model"
	"wardogs-mortar/internal/win32"
)

// Controller is the API exposed to the Wails control panel.
type Controller struct {
	app *application
}

type UIState struct {
	Current        *UICoordinate `json:"current,omitempty"`
	Target         *UICoordinate `json:"target,omitempty"`
	Distance       *float64      `json:"distance,omitempty"`
	Status         string        `json:"status"`
	Error          string        `json:"error"`
	OverlayVisible bool          `json:"overlayVisible"`
	BoundProcess   string        `json:"boundProcess"`
	OCRAvailable   bool          `json:"ocrAvailable"`
	Settings       UISettings    `json:"settings"`
}

type UICoordinate struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Source string  `json:"source"`
	Raw    string  `json:"raw"`
}

type UISettings struct {
	ReferenceWidth  int         `json:"referenceWidth"`
	ReferenceHeight int         `json:"referenceHeight"`
	ChatInput       config.Rect `json:"chatInput"`
	MapRegion       config.Rect `json:"mapRegion"`
	OverlayXPercent int         `json:"overlayXPercent"`
	OverlayYPercent int         `json:"overlayYPercent"`
}

func (c *Controller) GetState() UIState {
	return newUIState(c.app)
}

func (c *Controller) CaptureCurrent() {
	c.app.startCapture(coordinateCurrent)
}

func (c *Controller) CaptureTarget() {
	c.app.startCapture(coordinateTarget)
}

func (c *Controller) CalculateDistance() {
	c.app.calculateDistance()
}

func (c *Controller) StartCalibration() error {
	return c.app.startCalibration()
}

func (c *Controller) BindGame() error {
	window := win32.FindWardogsWindow("")
	if window == 0 {
		message := "未找到 Wardogs 窗口，请先启动游戏"
		c.app.setError(message)
		return fmt.Errorf("%s", message)
	}
	return c.app.bindWindow(window)
}

func (c *Controller) SetOverlayVisible(visible bool) error {
	c.app.mu.Lock()
	if c.app.cfg.OverlayVisible == visible {
		c.app.mu.Unlock()
		return nil
	}
	c.app.cfg.OverlayVisible = visible
	c.app.state.OverlayVisible = visible
	value := c.app.cfg
	c.app.mu.Unlock()
	if !visible {
		c.app.hideOverlay()
	}
	if err := config.Save(value); err != nil {
		c.app.setError("悬浮窗设置保存失败")
		return err
	}
	if visible {
		c.app.setStatus("操作提示浮窗已启用")
		c.app.showOverlayTemporarily()
	} else {
		c.app.setStatus("操作提示浮窗已关闭")
	}
	return nil
}

func (c *Controller) SaveSettings(settings UISettings) error {
	if err := validateSettings(settings); err != nil {
		c.app.setError(err.Error())
		return err
	}
	c.app.mu.Lock()
	c.app.cfg.ReferenceWidth = settings.ReferenceWidth
	c.app.cfg.ReferenceHeight = settings.ReferenceHeight
	c.app.cfg.ChatInput = settings.ChatInput
	c.app.cfg.MapRegion = settings.MapRegion
	c.app.cfg.OverlayXPercent = settings.OverlayXPercent
	c.app.cfg.OverlayYPercent = settings.OverlayYPercent
	value := c.app.cfg
	c.app.mu.Unlock()
	if err := config.Save(value); err != nil {
		c.app.setError("配置保存失败")
		return err
	}
	c.app.positionOverlay()
	c.app.setStatus("OCR 区域设置已保存")
	c.app.showOverlayTemporarily()
	return nil
}

func (c *Controller) ResetSettings() UIState {
	defaults := config.Default()
	c.app.mu.Lock()
	c.app.cfg.ReferenceWidth = defaults.ReferenceWidth
	c.app.cfg.ReferenceHeight = defaults.ReferenceHeight
	c.app.cfg.ChatInput = defaults.ChatInput
	c.app.cfg.MapRegion = defaults.MapRegion
	c.app.cfg.OverlayXPercent = defaults.OverlayXPercent
	c.app.cfg.OverlayYPercent = defaults.OverlayYPercent
	value := c.app.cfg
	c.app.mu.Unlock()
	if window := win32.FindWardogsWindow(value.BoundProcess); window != 0 {
		value = c.app.syncReferenceToMonitor(win32.MonitorRect(window))
	}
	if err := config.Save(value); err != nil {
		c.app.setError("默认配置保存失败")
	} else {
		c.app.positionOverlay()
		c.app.setStatus("已恢复默认设置")
		c.app.showOverlayTemporarily()
	}
	return newUIState(c.app)
}

func validateSettings(value UISettings) error {
	if value.ReferenceWidth <= 0 || value.ReferenceHeight <= 0 {
		return fmt.Errorf("参考分辨率必须大于 0")
	}
	if value.OverlayXPercent < 0 || value.OverlayXPercent > 100 || value.OverlayYPercent < 0 || value.OverlayYPercent > 100 {
		return fmt.Errorf("悬浮窗位置必须在 0 到 100 之间")
	}
	for name, rect := range map[string]config.Rect{"聊天输入框": value.ChatInput, "地图区域": value.MapRegion} {
		if rect.X < 0 || rect.Y < 0 || rect.W <= 0 || rect.H <= 0 || rect.X+rect.W > value.ReferenceWidth || rect.Y+rect.H > value.ReferenceHeight {
			return fmt.Errorf("%s超出参考分辨率范围", name)
		}
	}
	return nil
}

func newUIState(app *application) UIState {
	app.mu.RLock()
	defer app.mu.RUnlock()
	state := UIState{
		Distance:       copyFloat(app.state.Distance),
		Status:         app.state.Status,
		Error:          app.state.Error,
		OverlayVisible: app.state.OverlayVisible,
		BoundProcess:   app.cfg.BoundProcess,
		OCRAvailable:   app.ocr != nil,
		Settings: UISettings{
			ReferenceWidth:  app.cfg.ReferenceWidth,
			ReferenceHeight: app.cfg.ReferenceHeight,
			ChatInput:       app.cfg.ChatInput,
			MapRegion:       app.cfg.MapRegion,
			OverlayXPercent: app.cfg.OverlayXPercent,
			OverlayYPercent: app.cfg.OverlayYPercent,
		},
	}
	state.Current = uiCoordinate(app.state.Current)
	state.Target = uiCoordinate(app.state.Target)
	return state
}

func uiCoordinate(value *model.CoordinateResult) *UICoordinate {
	if value == nil {
		return nil
	}
	return &UICoordinate{X: value.Point.X, Y: value.Point.Y, Source: value.Source.String(), Raw: value.Raw}
}

func copyFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
