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
	systemocr "wardogs-mortar/internal/ocr"
	"wardogs-mortar/internal/win32"
)

const (
	hotkeyCurrent  = 1
	hotkeyTarget   = 2
	hotkeyDistance = 3

	menuBind   = 1001
	menuToggle = 1002
	menuExit   = 1003
	menuShow   = 1004

	wmClose     = 0x0010
	wmDestroy   = 0x0002
	wmRButtonUp = 0x0205
	wmLButtonUp = 0x0202
)

type coordinateKind int

const (
	coordinateCurrent coordinateKind = iota
	coordinateTarget
)

type application struct {
	mu                sync.RWMutex
	captureMu         sync.Mutex
	captureWG         sync.WaitGroup
	overlayMu         sync.Mutex
	overlayTimer      *time.Timer
	overlayGeneration uint64

	cfg     config.Config
	state   model.Snapshot
	main    *win32.Window
	overlay *win32.Window
	ocr     systemocr.OCRProvider
	ctx     context.Context
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
		Title:             "Wardogs 迫击炮控制台",
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
		_ = config.Save(a.cfg)
	}
	provider, ocrErr := systemocr.NewProvider()
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
	newConfig := a.cfg
	a.mu.Unlock()
	a.positionOverlay()
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
	a.positionOverlayOn(win32.MonitorRect(win32.ForegroundWindow()))
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
