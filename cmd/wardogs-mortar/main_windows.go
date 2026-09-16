//go:build windows && amd64

package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	windowsoptions "github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"wardogs-mortar/internal/config"
	"wardogs-mortar/internal/coords"
	"wardogs-mortar/internal/model"
	"wardogs-mortar/internal/ocr"
	"wardogs-mortar/internal/win32"
)

const (
	controlPanelTitle = "Wardogs 迫击炮控制台"

	hotkeyCurrent  = 1
	hotkeyTarget   = 2
	hotkeyDistance = 3

	menuBind   = 1001
	menuToggle = 1002
	menuExit   = 1003
	menuShow   = 1004

	wmClose       = 0x0010
	wmDestroy     = 0x0002
	wmEraseBkgnd  = 0x0014
	wmKeyDown     = 0x0100
	wmLButtonDown = 0x0201
	wmMouseMove   = 0x0200
	wmRButtonUp   = 0x0205
	wmLButtonUp   = 0x0202
)

type coordinateKind int

const (
	coordinateCurrent coordinateKind = iota
	coordinateTarget
)

type calibrationTarget int

const (
	calibrationNone calibrationTarget = iota
	calibrationChatMove
	calibrationChatResize
	calibrationMapMove
	calibrationMapResize
	calibrationOverlayMove
)

type calibrationHandle int

const (
	calibrationHandleNone calibrationHandle = iota
	calibrationHandleNW
	calibrationHandleNE
	calibrationHandleSW
	calibrationHandleSE
)

type calibrationDrag struct {
	target    calibrationTarget
	handle    calibrationHandle
	startX    int
	startY    int
	startRect win32.Rect
}

type calibrationSession struct {
	active        bool
	monitor       win32.Rect
	chat          win32.Rect
	mapRegion     win32.Rect
	overlay       win32.Rect
	original      config.Config
	controlWindow uintptr
	drag          calibrationDrag
	message       string
}

type application struct {
	mu                sync.RWMutex
	captureMu         sync.Mutex
	captureWG         sync.WaitGroup
	overlayMu         sync.Mutex
	overlayTimer      *time.Timer
	overlayGeneration uint64
	calibrationMu     sync.RWMutex

	cfg               config.Config
	state             model.Snapshot
	main              *win32.Window
	overlay           *win32.Window
	calibrationWindow *win32.Window
	calibration       calibrationSession
	ocr               ocr.OCRProvider
	ctx               context.Context
}

//go:embed frontend/dist
var frontendAssets embed.FS

func main() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	win32.SetDPIAware()

	app, err := newApplication()
	if err != nil {
		showFatal(err)
		return
	}
	defer app.close()
	assets, err := fs.Sub(frontendAssets, "frontend/dist")
	if err != nil {
		showFatal(err)
		return
	}
	controller := &Controller{app: app}
	if err := wails.Run(&options.App{
		Title:             controlPanelTitle,
		Width:             980,
		Height:            720,
		MinWidth:          820,
		MinHeight:         620,
		HideWindowOnClose: true,
		BackgroundColour:  &options.RGBA{R: 9, G: 13, B: 20, A: 1},
		AssetServer:       &assetserver.Options{Assets: assets},
		OnStartup:         app.startup,
		OnDomReady:        app.domReady,
		Bind:              []interface{}{controller},
		Windows:           &windowsoptions.Options{Theme: windowsoptions.Dark},
	}); err != nil {
		showFatal(err)
	}
}

func (a *application) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
}

func (a *application) domReady(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	a.emitState()
}

func newApplication() (*application, error) {
	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		cfg = config.Default()
	}
	a := &application{cfg: cfg}
	a.state.OverlayVisible = cfg.OverlayVisible
	a.state.Status = "F8 记录自己 · F9 记录目标 · F10 计算"
	if cfgErr != nil {
		a.state.Error = "配置读取失败，已使用默认配置"
	}

	mainWindow, err := win32.NewWindow(
		"WardogsMortarMessageWindow", "Wardogs Mortar Calculator",
		func(hwnd, message, wParam, lParam uintptr) uintptr {
			return a.mainProc(hwnd, message, wParam, lParam)
		},
		win32.ExToolWindow, win32.StyleOverlapped, 0, 0, 1, 1,
	)
	if err != nil {
		return nil, err
	}
	a.main = mainWindow
	win32.ShowWindow(a.main.Handle, win32.ShowHide)

	overlay, err := win32.NewWindow(
		"WardogsMortarOverlayWindow", "Wardogs Mortar Overlay",
		func(hwnd, message, wParam, lParam uintptr) uintptr {
			return a.overlayProc(hwnd, message, wParam, lParam)
		},
		win32.ExToolWindow|win32.ExLayered|win32.ExTransparent|win32.ExNoActivate|win32.ExTopmost,
		win32.StylePopup, 0, 0, 320, 126,
	)
	if err != nil {
		a.main.Destroy()
		return nil, err
	}
	a.overlay = overlay
	win32.SetLayeredAlpha(a.overlay.Handle, 225)
	a.positionOverlay()
	// The overlay is event-driven: successful capture/calculation shows it
	// briefly. It never stays visible just because the program is running.
	win32.ShowWindow(a.overlay.Handle, win32.ShowHide)

	calibrationWindow, err := win32.NewWindow(
		"WardogsMortarCalibrationWindow", "Wardogs Mortar Screen Calibration",
		func(hwnd, message, wParam, lParam uintptr) uintptr {
			return a.calibrationProc(hwnd, message, wParam, lParam)
		},
		win32.ExToolWindow|win32.ExLayered|win32.ExTopmost,
		win32.StylePopup, 0, 0, 1, 1,
	)
	if err != nil {
		a.overlay.Destroy()
		a.main.Destroy()
		return nil, err
	}
	a.calibrationWindow = calibrationWindow
	win32.SetLayeredAlpha(a.calibrationWindow.Handle, 165)
	win32.ShowWindow(a.calibrationWindow.Handle, win32.ShowHide)

	if err := win32.AddTrayIcon(a.main.Handle, 1, win32.TrayCallbackMessage, "Wardogs 迫击炮距离计算器"); err != nil {
		a.state.Error = "托盘图标创建失败"
	}
	for id, key := range map[int]uint32{hotkeyCurrent: 0x77, hotkeyTarget: 0x78, hotkeyDistance: 0x79} {
		if err := win32.RegisterHotKey(a.main.Handle, id, key); err != nil {
			a.state.Error = fmt.Sprintf("快捷键注册失败：%s", err)
		}
	}

	if foreground := win32.ForegroundWindow(); win32.IsWardogsWindow(foreground, "") {
		a.cfg.BoundProcess = win32.WindowProcessPath(foreground)
		syncedConfig := a.syncReferenceToMonitor(win32.MonitorRect(foreground))
		_ = config.Save(syncedConfig)
	} else if gameWindow := win32.FindWardogsWindow(a.cfg.BoundProcess); gameWindow != 0 {
		syncedConfig := a.syncReferenceToMonitor(win32.MonitorRect(gameWindow))
		_ = config.Save(syncedConfig)
	}
	provider, ocrErr := ocr.NewProvider()
	if ocrErr != nil {
		a.state.Error = "离线 OCR 不可用：" + ocrErr.Error()
	} else {
		a.ocr = provider
	}
	a.refreshOverlay()
	return a, nil
}

func (a *application) close() {
	// OCR jobs post their result back to the UI window. Let an in-flight
	// capture finish before tearing down the provider and both windows.
	a.captureWG.Wait()
	a.hideOverlay()
	if a.main != nil {
		win32.UnregisterHotKey(a.main.Handle, hotkeyCurrent)
		win32.UnregisterHotKey(a.main.Handle, hotkeyTarget)
		win32.UnregisterHotKey(a.main.Handle, hotkeyDistance)
		win32.RemoveTrayIcon(a.main.Handle, 1)
	}
	if a.ocr != nil {
		a.ocr.Close()
	}
	if a.overlay != nil {
		a.overlay.Destroy()
	}
	if a.calibrationWindow != nil {
		a.calibrationWindow.Destroy()
	}
	if a.main != nil {
		a.main.Destroy()
	}
}

func (a *application) mainProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch message {
	case win32.WMHotkey:
		if win32.IsWardogsWindow(win32.ForegroundWindow(), a.boundProcess()) {
			switch int(wParam) {
			case hotkeyCurrent:
				a.startCapture(coordinateCurrent)
			case hotkeyTarget:
				a.startCapture(coordinateTarget)
			case hotkeyDistance:
				a.calculateDistance()
			}
		}
		return 0
	case win32.TrayCallbackMessage:
		event := uint32(lParam)
		if event == wmRButtonUp || event == wmLButtonUp {
			a.showTrayMenu()
		}
		return 0
	case win32.WMAppUpdate:
		a.refreshOverlay()
		a.emitState()
		return 0
	case wmClose:
		win32.PostQuitMessage(0)
		return 0
	case wmDestroy:
		win32.PostQuitMessage(0)
		return 0
	}
	return win32.DefWindowProc(hwnd, message, wParam, lParam)
}

func (a *application) startCapture(kind coordinateKind) {
	a.captureWG.Add(1)
	go func() {
		defer a.captureWG.Done()
		a.captureCoordinate(kind)
	}()
}

func (a *application) startCalibration() error {
	a.calibrationMu.Lock()
	if a.calibration.active {
		a.calibrationMu.Unlock()
		return nil
	}
	a.calibrationMu.Unlock()

	a.mu.RLock()
	boundProcess := a.cfg.BoundProcess
	original := a.cfg
	ctx := a.ctx
	a.mu.RUnlock()
	gameWindow := win32.FindWardogsWindow(boundProcess)
	if gameWindow == 0 {
		return fmt.Errorf("未找到 Wardogs 窗口，请先启动游戏并点击绑定")
	}
	monitor := win32.MonitorRect(gameWindow)
	if monitor.Width() <= 0 || monitor.Height() <= 0 {
		return fmt.Errorf("无法读取游戏所在显示器分辨率")
	}
	if a.calibrationWindow == nil {
		return fmt.Errorf("屏幕校准窗口未初始化")
	}

	chat := localCalibrationRect(scaleRect(original.ChatInput, original.ReferenceWidth, original.ReferenceHeight, monitor), monitor)
	mapRegion := localCalibrationRect(scaleRect(original.MapRegion, original.ReferenceWidth, original.ReferenceHeight, monitor), monitor)
	overlay := calibrationOverlayRect(original.OverlayXPercent, original.OverlayYPercent, monitor)
	controlWindow := win32.FindWindowByTitle(controlPanelTitle)

	a.calibrationMu.Lock()
	if a.calibration.active {
		a.calibrationMu.Unlock()
		return nil
	}
	a.calibration = calibrationSession{
		active:        true,
		monitor:       monitor,
		chat:          clampCalibrationRect(chat, monitor.Width(), monitor.Height()),
		mapRegion:     clampCalibrationRect(mapRegion, monitor.Width(), monitor.Height()),
		overlay:       clampCalibrationRect(overlay, monitor.Width(), monitor.Height()),
		original:      original,
		controlWindow: controlWindow,
	}
	a.calibrationMu.Unlock()

	a.hideOverlay()
	if controlWindow != 0 {
		win32.ShowWindow(controlWindow, win32.ShowHide)
	}
	if ctx != nil {
		wailsruntime.WindowHide(ctx)
	}
	win32.SetWindowPosition(a.calibrationWindow.Handle, monitor.Left, monitor.Top, monitor.Width(), monitor.Height(), 0)
	win32.ShowWindow(a.calibrationWindow.Handle, win32.ShowNoActivate)
	win32.ActivateWindow(a.calibrationWindow.Handle)
	win32.SetFocus(a.calibrationWindow.Handle)
	win32.Invalidate(a.calibrationWindow.Handle)
	return nil
}

func (a *application) calibrationProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch message {
	case win32.WMPaint:
		hdc, paint := win32.BeginPaint(hwnd)
		if hdc != 0 {
			a.paintCalibration(hwnd, hdc)
		}
		win32.EndPaint(hwnd, paint)
		return 0
	case wmEraseBkgnd:
		return 1
	case wmLButtonDown:
		x, y := calibrationPoint(lParam)
		a.beginCalibrationDrag(hwnd, x, y)
		return 0
	case wmMouseMove:
		x, y := calibrationPoint(lParam)
		a.moveCalibrationDrag(x, y)
		return 0
	case wmLButtonUp:
		a.endCalibrationDrag()
		return 0
	case wmKeyDown:
		switch wParam {
		case 0x0D:
			a.finishCalibration(true)
			return 0
		case 0x1B:
			a.finishCalibration(false)
			return 0
		}
	case wmClose:
		a.finishCalibration(false)
		return 0
	case win32.WMNCHitTest:
		return win32.HTClient
	}
	return win32.DefWindowProc(hwnd, message, wParam, lParam)
}

func (a *application) paintCalibration(hwnd, hdc uintptr) {
	width, height := win32.ClientSize(hwnd)
	background := win32.TextRect{Left: 0, Top: 0, Right: int32(width), Bottom: int32(height)}
	win32.FillBlack(hdc, &background)

	a.calibrationMu.RLock()
	session := a.calibration
	a.calibrationMu.RUnlock()
	if !session.active {
		return
	}

	title := fmt.Sprintf("屏幕校准 %d×%d：拖动方框移动，拖四角缩放；Enter 保存，Esc 取消", session.monitor.Width(), session.monitor.Height())
	win32.DrawText(hdc, title, win32.TextRect{
		Left: 24, Top: 18, Right: int32(width - 24), Bottom: 48,
	}, 0x00FFFFFF, 18)
	drawCalibrationBox(hdc, session.chat, "聊天输入框", 0x00E8D849, 0x00304C50, true)
	drawCalibrationBox(hdc, session.mapRegion, "小地图 OCR 区域", 0x0051AAE7, 0x00302A18, true)
	drawCalibrationBox(hdc, session.overlay, "操作提示浮窗", 0x00A0E677, 0x00204430, false)
	if session.message != "" {
		win32.DrawText(hdc, truncate(session.message, 80), win32.TextRect{
			Left: 24, Top: int32(height - 42), Right: int32(width - 24), Bottom: int32(height - 16),
		}, 0x006080FF, 16)
	}
}

func drawCalibrationBox(hdc uintptr, rect win32.Rect, label string, border, fill uint32, handles bool) {
	area := calibrationTextRect(rect)
	win32.FillRectColor(hdc, area, fill)
	win32.DrawRectBorder(hdc, area, border, 2)
	win32.DrawText(hdc, label, win32.TextRect{
		Left: area.Left + 8, Top: area.Top + 6, Right: area.Right - 8, Bottom: area.Top + 28,
	}, 0x00FFFFFF, 16)
	if !handles {
		return
	}
	for _, point := range [][2]int32{
		{area.Left, area.Top}, {area.Right, area.Top}, {area.Left, area.Bottom}, {area.Right, area.Bottom},
	} {
		win32.FillRectColor(hdc, win32.TextRect{
			Left: point[0] - 5, Top: point[1] - 5, Right: point[0] + 5, Bottom: point[1] + 5,
		}, border)
	}
}

func calibrationPoint(lParam uintptr) (int, int) {
	return int(int16(uint16(lParam))), int(int16(uint16(lParam >> 16)))
}

func calibrationTextRect(rect win32.Rect) win32.TextRect {
	return win32.TextRect{Left: int32(rect.Left), Top: int32(rect.Top), Right: int32(rect.Right), Bottom: int32(rect.Bottom)}
}

func localCalibrationRect(rect, monitor win32.Rect) win32.Rect {
	return win32.Rect{
		Left: rect.Left - monitor.Left, Top: rect.Top - monitor.Top,
		Right: rect.Right - monitor.Left, Bottom: rect.Bottom - monitor.Top,
	}
}

func calibrationOverlayRect(xPercent, yPercent int, monitor win32.Rect) win32.Rect {
	const width, height = 320, 126
	availableWidth := max(0, monitor.Width()-width)
	availableHeight := max(0, monitor.Height()-height)
	return win32.Rect{
		Left:   availableWidth * clampPercent(xPercent) / 100,
		Top:    availableHeight * clampPercent(yPercent) / 100,
		Right:  availableWidth*clampPercent(xPercent)/100 + width,
		Bottom: availableHeight*clampPercent(yPercent)/100 + height,
	}
}

func clampCalibrationRect(rect win32.Rect, width, height int) win32.Rect {
	regionWidth := clampInt(rect.Width(), 1, max(1, width))
	regionHeight := clampInt(rect.Height(), 1, max(1, height))
	left := clampInt(rect.Left, 0, max(0, width-regionWidth))
	top := clampInt(rect.Top, 0, max(0, height-regionHeight))
	return win32.Rect{Left: left, Top: top, Right: left + regionWidth, Bottom: top + regionHeight}
}

func clampInt(value, low, high int) int {
	return max(low, min(value, high))
}

func (a *application) beginCalibrationDrag(hwnd uintptr, x, y int) {
	a.calibrationMu.Lock()
	defer a.calibrationMu.Unlock()
	if !a.calibration.active {
		return
	}
	target, handle := hitCalibrationTarget(a.calibration, x, y)
	if target == calibrationNone {
		return
	}
	startRect := a.calibration.chat
	if target == calibrationMapMove || target == calibrationMapResize {
		startRect = a.calibration.mapRegion
	}
	if target == calibrationOverlayMove {
		startRect = a.calibration.overlay
	}
	a.calibration.drag = calibrationDrag{target: target, handle: handle, startX: x, startY: y, startRect: startRect}
	win32.SetCapture(hwnd)
	win32.SetFocus(hwnd)
}

func (a *application) moveCalibrationDrag(x, y int) {
	a.calibrationMu.Lock()
	if !a.calibration.active || a.calibration.drag.target == calibrationNone {
		a.calibrationMu.Unlock()
		return
	}
	drag := a.calibration.drag
	dx, dy := x-drag.startX, y-drag.startY
	width, height := a.calibration.monitor.Width(), a.calibration.monitor.Height()
	switch drag.target {
	case calibrationChatMove:
		a.calibration.chat = moveCalibrationRect(drag.startRect, dx, dy, width, height)
	case calibrationChatResize:
		a.calibration.chat = resizeCalibrationRect(drag.startRect, dx, dy, drag.handle, width, height)
	case calibrationMapMove:
		a.calibration.mapRegion = moveCalibrationRect(drag.startRect, dx, dy, width, height)
	case calibrationMapResize:
		a.calibration.mapRegion = resizeCalibrationRect(drag.startRect, dx, dy, drag.handle, width, height)
	case calibrationOverlayMove:
		a.calibration.overlay = moveCalibrationRect(drag.startRect, dx, dy, width, height)
	}
	a.calibration.message = ""
	a.calibrationMu.Unlock()
	if a.calibrationWindow != nil {
		win32.Invalidate(a.calibrationWindow.Handle)
	}
}

func (a *application) endCalibrationDrag() {
	a.calibrationMu.Lock()
	a.calibration.drag = calibrationDrag{}
	a.calibrationMu.Unlock()
	win32.ReleaseCapture()
}

func hitCalibrationTarget(session calibrationSession, x, y int) (calibrationTarget, calibrationHandle) {
	if target, handle := hitCalibrationRegion(session.chat, x, y, calibrationChatMove, calibrationChatResize); target != calibrationNone {
		return target, handle
	}
	if target, handle := hitCalibrationRegion(session.mapRegion, x, y, calibrationMapMove, calibrationMapResize); target != calibrationNone {
		return target, handle
	}
	if pointInCalibrationRect(session.overlay, x, y) {
		return calibrationOverlayMove, calibrationHandleNone
	}
	return calibrationNone, calibrationHandleNone
}

func hitCalibrationRegion(rect win32.Rect, x, y int, moveTarget, resizeTarget calibrationTarget) (calibrationTarget, calibrationHandle) {
	if handle := calibrationHandleAt(rect, x, y); handle != calibrationHandleNone {
		return resizeTarget, handle
	}
	if pointInCalibrationRect(rect, x, y) {
		return moveTarget, calibrationHandleNone
	}
	return calibrationNone, calibrationHandleNone
}

func calibrationHandleAt(rect win32.Rect, x, y int) calibrationHandle {
	const radius = 10
	nearLeft, nearRight := absInt(x-rect.Left) <= radius, absInt(x-rect.Right) <= radius
	nearTop, nearBottom := absInt(y-rect.Top) <= radius, absInt(y-rect.Bottom) <= radius
	switch {
	case nearLeft && nearTop:
		return calibrationHandleNW
	case nearRight && nearTop:
		return calibrationHandleNE
	case nearLeft && nearBottom:
		return calibrationHandleSW
	case nearRight && nearBottom:
		return calibrationHandleSE
	default:
		return calibrationHandleNone
	}
}

func pointInCalibrationRect(rect win32.Rect, x, y int) bool {
	return x >= rect.Left && x <= rect.Right && y >= rect.Top && y <= rect.Bottom
}

func moveCalibrationRect(start win32.Rect, dx, dy, width, height int) win32.Rect {
	left := clampInt(start.Left+dx, 0, max(0, width-start.Width()))
	top := clampInt(start.Top+dy, 0, max(0, height-start.Height()))
	return win32.Rect{Left: left, Top: top, Right: left + start.Width(), Bottom: top + start.Height()}
}

func resizeCalibrationRect(start win32.Rect, dx, dy int, handle calibrationHandle, width, height int) win32.Rect {
	const minimum = 12
	left, top, right, bottom := start.Left, start.Top, start.Right, start.Bottom
	switch handle {
	case calibrationHandleNW:
		left = clampInt(start.Left+dx, 0, right-minimum)
		top = clampInt(start.Top+dy, 0, bottom-minimum)
	case calibrationHandleNE:
		right = clampInt(start.Right+dx, left+minimum, width)
		top = clampInt(start.Top+dy, 0, bottom-minimum)
	case calibrationHandleSW:
		left = clampInt(start.Left+dx, 0, right-minimum)
		bottom = clampInt(start.Bottom+dy, top+minimum, height)
	case calibrationHandleSE:
		right = clampInt(start.Right+dx, left+minimum, width)
		bottom = clampInt(start.Bottom+dy, top+minimum, height)
	}
	return win32.Rect{Left: left, Top: top, Right: right, Bottom: bottom}
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func (a *application) finishCalibration(save bool) {
	a.calibrationMu.Lock()
	if !a.calibration.active {
		a.calibrationMu.Unlock()
		return
	}
	session := a.calibration
	a.calibrationMu.Unlock()

	if save {
		value := calibrationConfig(session)
		if err := config.Save(value); err != nil {
			a.calibrationMu.Lock()
			a.calibration.message = "保存失败：" + err.Error()
			a.calibrationMu.Unlock()
			win32.Invalidate(a.calibrationWindow.Handle)
			return
		}
		a.mu.Lock()
		a.cfg = value
		a.state.Status = "屏幕校准已保存"
		a.state.Error = ""
		a.mu.Unlock()
	} else {
		a.mu.Lock()
		a.state.Status = "已取消屏幕校准"
		a.state.Error = ""
		a.mu.Unlock()
	}

	a.calibrationMu.Lock()
	a.calibration.active = false
	a.calibration.drag = calibrationDrag{}
	controlWindow := a.calibration.controlWindow
	a.calibration.controlWindow = 0
	a.calibrationMu.Unlock()
	win32.ReleaseCapture()
	win32.ShowWindow(a.calibrationWindow.Handle, win32.ShowHide)
	if controlWindow != 0 {
		win32.ShowWindow(controlWindow, win32.ShowNoActivate)
	}
	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()
	if ctx != nil {
		wailsruntime.WindowShow(ctx)
		wailsruntime.WindowUnminimise(ctx)
	}
	if save {
		a.positionOverlayOn(session.monitor)
	}
	a.postUpdate()
}

func calibrationConfig(session calibrationSession) config.Config {
	width, height := session.monitor.Width(), session.monitor.Height()
	value := session.original
	value.ReferenceWidth = width
	value.ReferenceHeight = height
	value.ChatInput = configRectFromCalibration(session.chat)
	value.MapRegion = configRectFromCalibration(session.mapRegion)
	availableWidth := max(0, width-session.overlay.Width())
	availableHeight := max(0, height-session.overlay.Height())
	value.OverlayXPercent = percentFromCalibration(session.overlay.Left, availableWidth)
	value.OverlayYPercent = percentFromCalibration(session.overlay.Top, availableHeight)
	return value
}

func configRectFromCalibration(rect win32.Rect) config.Rect {
	return config.Rect{X: rect.Left, Y: rect.Top, W: rect.Width(), H: rect.Height()}
}

func percentFromCalibration(value, available int) int {
	if available <= 0 {
		return 0
	}
	return clampPercent(value * 100 / available)
}

func (a *application) overlayProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch message {
	case win32.WMPaint:
		hdc, paint := win32.BeginPaint(hwnd)
		if hdc != 0 {
			a.paintOverlay(hwnd, hdc)
		}
		win32.EndPaint(hwnd, paint)
		return 0
	case win32.WMNCHitTest:
		return win32.HTTransparent
	case win32.WMMouseActivate:
		return win32.MANoActivate
	}
	return win32.DefWindowProc(hwnd, message, wParam, lParam)
}

func (a *application) paintOverlay(hwnd, hdc uintptr) {
	width, height := win32.ClientSize(hwnd)
	background := win32.TextRect{Left: 0, Top: 0, Right: int32(width), Bottom: int32(height)}
	win32.FillBlack(hdc, &background)

	snapshot := a.snapshot()
	lines := []struct {
		text  string
		color uint32
	}{
		{text: formatCoordinateLine("自己", snapshot.Current), color: 0x00FFFFFF},
		{text: formatCoordinateLine("目标", snapshot.Target), color: 0x00FFFFFF},
		{text: formatDistanceLine(snapshot), color: 0x0050FF80},
	}
	if snapshot.Error != "" {
		lines = append(lines, struct {
			text  string
			color uint32
		}{text: truncate(snapshot.Error, 36), color: 0x006080FF})
	} else if snapshot.Status != "" {
		lines = append(lines, struct {
			text  string
			color uint32
		}{text: truncate(snapshot.Status, 36), color: 0x00C0C0C0})
	}

	for index, line := range lines {
		top := int32(8 + index*27)
		win32.DrawText(hdc, line.text, win32.TextRect{Left: 12, Top: top, Right: int32(width - 12), Bottom: top + 25}, line.color, 18)
	}
}

func formatCoordinateLine(label string, result *model.CoordinateResult) string {
	if result == nil {
		return label + "：未记录"
	}
	return fmt.Sprintf("%s：x%.2f, y%.2f [%s]", label, result.Point.X, result.Point.Y, result.Source)
}

func formatDistanceLine(snapshot model.Snapshot) string {
	if snapshot.Distance == nil {
		return "距离：未计算"
	}
	return fmt.Sprintf("距离：%.0f m", *snapshot.Distance)
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

func (a *application) captureCoordinate(kind coordinateKind) {
	a.captureMu.Lock()
	defer a.captureMu.Unlock()

	a.setStatus("正在识别坐标…")
	if a.ocr == nil {
		a.setError("系统 OCR 未初始化")
		return
	}
	foreground := win32.ForegroundWindow()
	a.mu.RLock()
	boundProcess := a.cfg.BoundProcess
	a.mu.RUnlock()
	gameWindow := foreground
	if !win32.IsWardogsWindow(gameWindow, boundProcess) {
		gameWindow = win32.FindWardogsWindow(boundProcess)
	}
	if gameWindow == 0 {
		a.setError("未找到 Wardogs 窗口，请先启动游戏并点击绑定")
		return
	}
	monitor := win32.MonitorRect(gameWindow)
	a.positionOverlayOn(monitor)
	a.mu.RLock()
	chatConfig := a.cfg.ChatInput
	mapConfig := a.cfg.MapRegion
	referenceWidth := a.cfg.ReferenceWidth
	referenceHeight := a.cfg.ReferenceHeight
	a.mu.RUnlock()
	chatRect := scaleRect(chatConfig, referenceWidth, referenceHeight, monitor)
	mapRect := scaleRect(mapConfig, referenceWidth, referenceHeight, monitor)

	// Both actions prefer the explicit coordinate typed in chat. A brief
	// second attempt prevents a transient frame/OCR miss from silently
	// selecting the map coordinate instead.
	result, err := a.readFromRegion(chatRect, model.SourceChatInput)
	if err != nil {
		time.Sleep(60 * time.Millisecond)
		result, err = a.readFromRegion(chatRect, model.SourceChatInput)
	}
	if err != nil {
		result, err = a.readFromRegion(mapRect, model.SourceMapCursor)
	}
	if err != nil {
		a.setError("未在聊天输入行或地图区域识别到完整坐标（需要 x 和 y）")
		return
	}

	a.mu.Lock()
	if kind == coordinateCurrent {
		a.state.RecordCurrent(result)
		a.state.Status = "已记录自己坐标"
	} else {
		a.state.RecordTarget(result)
		a.state.Status = "已记录目标坐标"
	}
	a.state.Error = ""
	a.mu.Unlock()
	a.postUpdate()
	a.showOverlayTemporarily()
}

func (a *application) readFromRegion(rect win32.Rect, source model.CoordinateSource) (model.CoordinateResult, error) {
	image, err := win32.CaptureRect(rect)
	if err != nil {
		return model.CoordinateResult{}, err
	}
	text, err := a.ocr.Recognize(image)
	if err != nil {
		return model.CoordinateResult{}, err
	}
	point, ok := coords.ParseCoordinate(text)
	if !ok {
		return model.CoordinateResult{}, fmt.Errorf("no coordinate in %s", source)
	}
	return model.CoordinateResult{Point: point, Source: source, Raw: text}, nil
}

func (a *application) calculateDistance() {
	a.mu.Lock()
	calculated := a.state.CalculateDistance()
	if !calculated {
		a.state.Error = "请先用 F8 和 F9 记录两组坐标"
		a.state.Status = "无法计算"
	} else {
		a.state.Error = ""
		a.state.Status = "已计算距离"
	}
	a.mu.Unlock()
	a.postUpdate()
	if calculated {
		a.showOverlayTemporarily()
	}
}
func (a *application) showTrayMenu() {
	foreground := win32.ForegroundWindow()
	selected := win32.ShowPopupMenu(a.main.Handle, []win32.MenuItem{
		{ID: menuShow, Label: "打开控制台"},
		{ID: menuBind, Label: "绑定当前前台窗口"},
		{ID: menuToggle, Label: "开启/关闭操作提示浮窗"},
		{ID: menuExit, Label: "退出"},
	})
	switch selected {
	case menuShow:
		a.showControlPanel()
	case menuBind:
		_ = a.bindWindow(foreground)
	case menuToggle:
		a.toggleOverlay()
	case menuExit:
		a.quit()
	}
}

func (a *application) bindWindow(window uintptr) error {
	if window == 0 || window == a.main.Handle || window == a.overlay.Handle {
		a.setError("请先激活 Wardogs，再选择绑定")
		return fmt.Errorf("请先激活 Wardogs，再选择绑定")
	}
	path := win32.WindowProcessPath(window)
	if path == "" {
		a.setError("无法读取前台窗口进程")
		return fmt.Errorf("无法读取前台窗口进程")
	}
	a.mu.Lock()
	a.cfg.BoundProcess = path
	a.mu.Unlock()
	monitor := win32.MonitorRect(window)
	newConfig := a.syncReferenceToMonitor(monitor)
	a.positionOverlayOn(monitor)
	if err := config.Save(newConfig); err != nil {
		a.setError("绑定保存失败")
		return err
	}
	a.setStatus("已绑定：" + win32.WindowTitle(window))
	return nil
}

func (a *application) toggleOverlay() {
	a.mu.Lock()
	a.cfg.OverlayVisible = !a.cfg.OverlayVisible
	visible := a.cfg.OverlayVisible
	a.state.OverlayVisible = visible
	newConfig := a.cfg
	a.mu.Unlock()
	if visible {
		a.showOverlayTemporarily()
	} else {
		a.hideOverlay()
	}
	_ = config.Save(newConfig)
}

func (a *application) positionOverlay() {
	window := win32.FindWardogsWindow(a.boundProcess())
	if window == 0 {
		window = win32.ForegroundWindow()
	}
	a.positionOverlayOn(win32.MonitorRect(window))
}

func (a *application) positionOverlayOn(monitor win32.Rect) {
	width, height := 320, 126
	a.mu.RLock()
	xPercent := clampPercent(a.cfg.OverlayXPercent)
	yPercent := clampPercent(a.cfg.OverlayYPercent)
	a.mu.RUnlock()
	left := monitor.Left + max(0, monitor.Width()-width)*xPercent/100
	top := monitor.Top + max(0, monitor.Height()-height)*yPercent/100
	win32.SetWindowPosition(a.overlay.Handle, left, top, width, height, win32.SWPNoActivate)
}

func clampPercent(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func (a *application) showOverlayTemporarily() {
	a.mu.RLock()
	enabled := a.cfg.OverlayVisible
	a.mu.RUnlock()
	if !enabled || a.overlay == nil {
		return
	}

	a.refreshOverlay()
	win32.ShowWindow(a.overlay.Handle, win32.ShowNoActivate)
	a.overlayMu.Lock()
	a.overlayGeneration++
	generation := a.overlayGeneration
	if a.overlayTimer != nil {
		a.overlayTimer.Stop()
	}
	a.overlayTimer = time.AfterFunc(3*time.Second, func() {
		a.overlayMu.Lock()
		defer a.overlayMu.Unlock()
		if a.overlayGeneration == generation && a.overlay != nil {
			win32.ShowWindow(a.overlay.Handle, win32.ShowHide)
		}
	})
	a.overlayMu.Unlock()
}

func (a *application) hideOverlay() {
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	a.overlayGeneration++
	if a.overlayTimer != nil {
		a.overlayTimer.Stop()
		a.overlayTimer = nil
	}
	if a.overlay != nil {
		win32.ShowWindow(a.overlay.Handle, win32.ShowHide)
	}
}

func (a *application) boundProcess() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.BoundProcess
}

func (a *application) setStatus(status string) {
	a.mu.Lock()
	a.state.Status = status
	a.state.Error = ""
	a.mu.Unlock()
	a.postUpdate()
}

func (a *application) setError(message string) {
	a.mu.Lock()
	a.state.Error = message
	a.mu.Unlock()
	a.postUpdate()
}

func (a *application) postUpdate() {
	if a.main != nil {
		win32.PostMessage(a.main.Handle, win32.WMAppUpdate, 0, 0)
	}
}

func (a *application) emitState() {
	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()
	if ctx != nil {
		wailsruntime.EventsEmit(ctx, "state:update", newUIState(a))
	}
}

func (a *application) showControlPanel() {
	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()
	if ctx != nil {
		wailsruntime.WindowShow(ctx)
		wailsruntime.WindowUnminimise(ctx)
		wailsruntime.WindowSetAlwaysOnTop(ctx, true)
		wailsruntime.WindowSetAlwaysOnTop(ctx, false)
	}
}

func (a *application) quit() {
	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()
	if ctx != nil {
		wailsruntime.Quit(ctx)
		return
	}
	win32.PostMessage(a.main.Handle, wmClose, 0, 0)
}

func (a *application) refreshOverlay() {
	if a.overlay != nil {
		win32.Invalidate(a.overlay.Handle)
	}
}

func (a *application) snapshot() model.Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	copySnapshot := a.state
	if a.state.Current != nil {
		value := *a.state.Current
		copySnapshot.Current = &value
	}
	if a.state.Target != nil {
		value := *a.state.Target
		copySnapshot.Target = &value
	}
	if a.state.Distance != nil {
		value := *a.state.Distance
		copySnapshot.Distance = &value
	}
	return copySnapshot
}

func (a *application) syncReferenceToMonitor(monitor win32.Rect) config.Config {
	width, height := monitor.Width(), monitor.Height()
	a.mu.Lock()
	defer a.mu.Unlock()
	if width <= 0 || height <= 0 {
		return a.cfg
	}
	if a.cfg.ReferenceWidth != width || a.cfg.ReferenceHeight != height {
		a.cfg.ChatInput = rescaleConfigRect(a.cfg.ChatInput, a.cfg.ReferenceWidth, a.cfg.ReferenceHeight, width, height)
		a.cfg.MapRegion = rescaleConfigRect(a.cfg.MapRegion, a.cfg.ReferenceWidth, a.cfg.ReferenceHeight, width, height)
		a.cfg.ReferenceWidth = width
		a.cfg.ReferenceHeight = height
	}
	return a.cfg
}

func rescaleConfigRect(value config.Rect, fromWidth, fromHeight, toWidth, toHeight int) config.Rect {
	if fromWidth <= 0 || fromHeight <= 0 || toWidth <= 0 || toHeight <= 0 {
		return value
	}
	return config.Rect{
		X: value.X * toWidth / fromWidth,
		Y: value.Y * toHeight / fromHeight,
		W: value.W * toWidth / fromWidth,
		H: value.H * toHeight / fromHeight,
	}
}

func scaleRect(value config.Rect, referenceWidth, referenceHeight int, monitor win32.Rect) win32.Rect {
	if referenceWidth <= 0 {
		referenceWidth = monitor.Width()
	}
	if referenceHeight <= 0 {
		referenceHeight = monitor.Height()
	}
	left := monitor.Left + value.X*monitor.Width()/referenceWidth
	top := monitor.Top + value.Y*monitor.Height()/referenceHeight
	width := value.W * monitor.Width() / referenceWidth
	height := value.H * monitor.Height() / referenceHeight
	return win32.Rect{Left: left, Top: top, Right: left + width, Bottom: top + height}
}

func showFatal(err error) {
	// The normal build uses the GUI subsystem, so a fatal startup error is
	// intentionally sent to the Windows message box instead of stderr.
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBox := user32.NewProc("MessageBoxW")
	title, _ := syscall.UTF16PtrFromString("Wardogs Mortar Calculator")
	text, _ := syscall.UTF16PtrFromString(err.Error())
	messageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}
