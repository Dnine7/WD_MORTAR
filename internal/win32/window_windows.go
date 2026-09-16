//go:build windows && amd64

package win32

import (
	"fmt"
	"syscall"
	"unsafe"
)

type WindowProc func(hwnd, msg, wParam, lParam uintptr) uintptr

type Window struct {
	Handle   uintptr
	callback uintptr
}

type point struct {
	X int32
	Y int32
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   point
	Private uint32
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	SmallIcon  uintptr
}

type nativeWindowRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

// TextRect is a Win32 rectangle used when painting overlay text.
type TextRect = nativeWindowRect

var (
	windowUser32   = syscall.NewLazyDLL("user32.dll")
	windowGDI32    = syscall.NewLazyDLL("gdi32.dll")
	windowShell32  = syscall.NewLazyDLL("shell32.dll")
	windowKernel32 = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClassEx      = windowUser32.NewProc("RegisterClassExW")
	procCreateWindowEx       = windowUser32.NewProc("CreateWindowExW")
	procDefWindowProc        = windowUser32.NewProc("DefWindowProcW")
	procDestroyWindow        = windowUser32.NewProc("DestroyWindow")
	procShowWindow           = windowUser32.NewProc("ShowWindow")
	procUpdateWindow         = windowUser32.NewProc("UpdateWindow")
	procInvalidateRect       = windowUser32.NewProc("InvalidateRect")
	procSetWindowPos         = windowUser32.NewProc("SetWindowPos")
	procSetLayeredAttributes = windowUser32.NewProc("SetLayeredWindowAttributes")
	procGetMessage           = windowUser32.NewProc("GetMessageW")
	procTranslateMessage     = windowUser32.NewProc("TranslateMessage")
	procDispatchMessage      = windowUser32.NewProc("DispatchMessageW")
	procPostMessage          = windowUser32.NewProc("PostMessageW")
	procPostQuitMessage      = windowUser32.NewProc("PostQuitMessage")
	procRegisterHotKey       = windowUser32.NewProc("RegisterHotKey")
	procUnregisterHotKey     = windowUser32.NewProc("UnregisterHotKey")
	procGetCursorPos         = windowUser32.NewProc("GetCursorPos")
	procSetForegroundWindow  = windowUser32.NewProc("SetForegroundWindow")
	procSetFocus             = windowUser32.NewProc("SetFocus")
	procSetCapture           = windowUser32.NewProc("SetCapture")
	procReleaseCapture       = windowUser32.NewProc("ReleaseCapture")
	procCreatePopupMenu      = windowUser32.NewProc("CreatePopupMenu")
	procAppendMenu           = windowUser32.NewProc("AppendMenuW")
	procTrackPopupMenu       = windowUser32.NewProc("TrackPopupMenu")
	procDestroyMenu          = windowUser32.NewProc("DestroyMenu")
	procLoadIcon             = windowUser32.NewProc("LoadIconW")
	procLoadCursor           = windowUser32.NewProc("LoadCursorW")
	procGetModuleHandle      = windowKernel32.NewProc("GetModuleHandleW")
	procShellNotifyIcon      = windowShell32.NewProc("Shell_NotifyIconW")

	procBeginPaint       = windowUser32.NewProc("BeginPaint")
	procEndPaint         = windowUser32.NewProc("EndPaint")
	procGetClientRect    = windowUser32.NewProc("GetClientRect")
	procFillRect         = windowUser32.NewProc("FillRect")
	procSetBkMode        = windowGDI32.NewProc("SetBkMode")
	procSetTextColor     = windowGDI32.NewProc("SetTextColor")
	procCreateSolidBrush = windowGDI32.NewProc("CreateSolidBrush")
	procCreateFont       = windowGDI32.NewProc("CreateFontW")
	procSelectObjectGDI  = windowGDI32.NewProc("SelectObject")
	procDeleteObjectGDI  = windowGDI32.NewProc("DeleteObject")
	procDrawText         = windowUser32.NewProc("DrawTextW")
)

const (
	wsOverlappedWindow = 0x00CF0000
	wsPopup            = 0x80000000
	wsExToolWindow     = 0x00000080
	wsExLayered        = 0x00080000
	wsExTransparent    = 0x00000020
	wsExNoActivate     = 0x08000000
	wsExTopmost        = 0x00000008

	swHide           = 0
	swShowNoActivate = 4

	hwndTopmost   = ^uintptr(0)
	swpNoActivate = 0x0010
	swpShowWindow = 0x0040
	swpNoSize     = 0x0001
	swpNoMove     = 0x0002

	lwaAlpha    = 0x2
	transparent = 1

	wmPaint         = 0x000F
	wmDestroy       = 0x0002
	wmNcHitTest     = 0x0084
	wmMouseActivate = 0x0021
	htClient        = 1
	htTransparent   = ^uintptr(0) // -1
	maNoActivate    = 3

	wmApp = 0x8000

	modNoRepeat   = 0x4000
	pmRetCmd      = 0x0100
	pmRightButton = 0x0002

	nimAdd     = 0
	nimDelete  = 2
	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4

	idiApplication  = 32512
	appIconID       = 3 // Wails embeds the application icon as resource ID 3.
	iconCallbackMsg = wmApp + 20

	// Exported aliases used by the application package.
	StylePopup          = wsPopup
	StyleOverlapped     = wsOverlappedWindow
	ExToolWindow        = wsExToolWindow
	ExLayered           = wsExLayered
	ExTransparent       = wsExTransparent
	ExNoActivate        = wsExNoActivate
	ExTopmost           = wsExTopmost
	ShowHide            = swHide
	ShowNoActivate      = swShowNoActivate
	WMPaint             = wmPaint
	WMNCHitTest         = wmNcHitTest
	WMMouseActivate     = wmMouseActivate
	HTClient            = htClient
	HTTransparent       = htTransparent
	MANoActivate        = maNoActivate
	WMHotkey            = 0x0312
	WMAppUpdate         = wmApp + 1
	TrayCallbackMessage = iconCallbackMsg
	SWPNoActivate       = swpNoActivate
	SWPNoSize           = swpNoSize
	SWPNoMove           = swpNoMove
)

type paintStruct struct {
	Hdc       uintptr
	Erase     int32
	Paint     nativeWindowRect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	Flags            uint32
	CallbackMessage  uint32
	Icon             uintptr
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	TimeoutOrVersion uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GuidItem         [16]byte
	BalloonIcon      uintptr
}

func DefWindowProc(hwnd, message, wParam, lParam uintptr) uintptr {
	ret, _, _ := procDefWindowProc.Call(hwnd, message, wParam, lParam)
	return ret
}

func loadAppIcon(instance uintptr) uintptr {
	icon, _, _ := procLoadIcon.Call(instance, appIconID)
	if icon == 0 {
		icon, _, _ = procLoadIcon.Call(0, idiApplication)
	}
	return icon
}

func NewWindow(className, title string, proc WindowProc, exStyle, style uint32, x, y, width, height int) (*Window, error) {
	classPtr, err := syscall.UTF16PtrFromString(className)
	if err != nil {
		return nil, err
	}
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return nil, err
	}
	callback := syscall.NewCallback(func(hwnd, message, wParam, lParam uintptr) uintptr {
		return proc(hwnd, message, wParam, lParam)
	})
	instance, _, _ := procGetModuleHandle.Call(0)
	cursor, _, _ := procLoadCursor.Call(0, idiApplication)
	icon := loadAppIcon(instance)
	class := wndClassEx{
		Size: uint32(unsafe.Sizeof(wndClassEx{})), WndProc: callback,
		Instance: instance, Cursor: cursor, Icon: icon, SmallIcon: icon,
		ClassName: classPtr,
	}
	if atom, _, callErr := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&class))); atom == 0 && callErr != syscall.Errno(1410) {
		return nil, fmt.Errorf("register window class: %w", callErr)
	}
	hwnd, _, callErr := procCreateWindowEx.Call(
		uintptr(exStyle), uintptr(unsafe.Pointer(classPtr)), uintptr(unsafe.Pointer(titlePtr)),
		uintptr(style), uintptr(int32(x)), uintptr(int32(y)), uintptr(width), uintptr(height),
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return nil, fmt.Errorf("create window: %w", callErr)
	}
	return &Window{Handle: hwnd, callback: callback}, nil
}

func (w *Window) Destroy() {
	if w != nil && w.Handle != 0 {
		procDestroyWindow.Call(w.Handle)
		w.Handle = 0
	}
}

func ShowWindow(hwnd uintptr, command int) {
	procShowWindow.Call(hwnd, uintptr(command))
}

func ActivateWindow(hwnd uintptr) {
	if hwnd != 0 {
		procSetForegroundWindow.Call(hwnd)
	}
}

func SetFocus(hwnd uintptr) {
	if hwnd != 0 {
		procSetFocus.Call(hwnd)
	}
}

func SetCapture(hwnd uintptr) {
	if hwnd != 0 {
		procSetCapture.Call(hwnd)
	}
}

func ReleaseCapture() {
	procReleaseCapture.Call()
}

func UpdateWindow(hwnd uintptr) {
	procUpdateWindow.Call(hwnd)
}

func Invalidate(hwnd uintptr) {
	procInvalidateRect.Call(hwnd, 0, 1)
}

func SetLayeredAlpha(hwnd uintptr, alpha byte) {
	procSetLayeredAttributes.Call(hwnd, 0, uintptr(alpha), lwaAlpha)
}

func SetWindowPosition(hwnd uintptr, x, y, width, height int, flags uintptr) {
	procSetWindowPos.Call(hwnd, hwndTopmost, uintptr(int32(x)), uintptr(int32(y)), uintptr(width), uintptr(height), flags)
}

func MessageLoop() error {
	for {
		var message msg
		ret, _, callErr := procGetMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(ret) == -1 {
			return fmt.Errorf("message loop: %w", callErr)
		}
		if ret == 0 {
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func PostMessage(hwnd uintptr, message, wParam, lParam uintptr) {
	procPostMessage.Call(hwnd, message, wParam, lParam)
}

func PostQuitMessage(exitCode int) {
	procPostQuitMessage.Call(uintptr(exitCode))
}

func RegisterHotKey(hwnd uintptr, id int, virtualKey uint32) error {
	ret, _, err := procRegisterHotKey.Call(hwnd, uintptr(id), modNoRepeat, uintptr(virtualKey))
	if ret == 0 {
		return fmt.Errorf("register hotkey %d: %w", virtualKey, err)
	}
	return nil
}

func UnregisterHotKey(hwnd uintptr, id int) {
	procUnregisterHotKey.Call(hwnd, uintptr(id))
}

func CursorPosition() (int, int) {
	var value point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&value)))
	return int(value.X), int(value.Y)
}

func ShowPopupMenu(hwnd uintptr, items []MenuItem) int {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return 0
	}
	defer procDestroyMenu.Call(menu)
	for _, item := range items {
		label, err := syscall.UTF16PtrFromString(item.Label)
		if err != nil {
			continue
		}
		procAppendMenu.Call(menu, uintptr(item.Flags), uintptr(item.ID), uintptr(unsafe.Pointer(label)))
	}
	x, y := CursorPosition()
	procSetForegroundWindow.Call(hwnd)
	result, _, _ := procTrackPopupMenu.Call(menu, pmRetCmd|pmRightButton, uintptr(x), uintptr(y), 0, hwnd, 0)
	return int(result)
}

type MenuItem struct {
	ID    uint32
	Label string
	Flags uint32
}

func BeginPaint(hwnd uintptr) (uintptr, *paintStruct) {
	paint := new(paintStruct)
	hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(paint)))
	return hdc, paint
}

func EndPaint(hwnd uintptr, paint *paintStruct) {
	procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(paint)))
}

func ClientSize(hwnd uintptr) (int, int) {
	var rect nativeWindowRect
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	return int(rect.Right - rect.Left), int(rect.Bottom - rect.Top)
}

func FillBlack(hdc uintptr, rect *TextRect) {
	FillRectColor(hdc, *rect, 0x00000000)
}

func FillRectColor(hdc uintptr, rect TextRect, color uint32) {
	brush, _, _ := procCreateSolidBrush.Call(uintptr(color))
	if brush == 0 {
		return
	}
	defer procDeleteObjectGDI.Call(brush)
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&rect)), brush)
}

func DrawRectBorder(hdc uintptr, rect TextRect, color uint32, thickness int) {
	if thickness < 1 {
		thickness = 1
	}
	FillRectColor(hdc, TextRect{Left: rect.Left, Top: rect.Top, Right: rect.Right, Bottom: rect.Top + int32(thickness)}, color)
	FillRectColor(hdc, TextRect{Left: rect.Left, Top: rect.Bottom - int32(thickness), Right: rect.Right, Bottom: rect.Bottom}, color)
	FillRectColor(hdc, TextRect{Left: rect.Left, Top: rect.Top, Right: rect.Left + int32(thickness), Bottom: rect.Bottom}, color)
	FillRectColor(hdc, TextRect{Left: rect.Right - int32(thickness), Top: rect.Top, Right: rect.Right, Bottom: rect.Bottom}, color)
}

func DrawText(hdc uintptr, text string, rect TextRect, color uint32, height int) {
	textPtr, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	fontName, _ := syscall.UTF16PtrFromString("Microsoft YaHei UI")
	font, _, _ := procCreateFont.Call(
		uintptr(int32(-height)), 0, 0, 0, 500, 0, 0, 0,
		1, 0, 0, 0, 0, uintptr(unsafe.Pointer(fontName)),
	)
	if font == 0 {
		return
	}
	defer procDeleteObjectGDI.Call(font)
	oldFont, _, _ := procSelectObjectGDI.Call(hdc, font)
	defer procSelectObjectGDI.Call(hdc, oldFont)
	procSetBkMode.Call(hdc, transparent)
	procSetTextColor.Call(hdc, uintptr(color))
	procDrawText.Call(hdc, uintptr(unsafe.Pointer(textPtr)), ^uintptr(0), uintptr(unsafe.Pointer(&rect)), 0x00000000)
}

func AddTrayIcon(hwnd uintptr, id uint32, message uint32, tip string) error {
	data := notifyIconData{
		CbSize:          uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:            hwnd,
		UID:             id,
		Flags:           nifMessage | nifIcon | nifTip,
		CallbackMessage: message,
	}
	instance, _, _ := procGetModuleHandle.Call(0)
	data.Icon = loadAppIcon(instance)
	copy(data.Tip[:], mustUTF16(tip))
	ret, _, err := procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&data)))
	if ret == 0 {
		return fmt.Errorf("add tray icon: %w", err)
	}
	return nil
}

func RemoveTrayIcon(hwnd uintptr, id uint32) {
	data := notifyIconData{
		CbSize: uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:   hwnd,
		UID:    id,
	}
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
}

func mustUTF16(value string) []uint16 {
	encoded, err := syscall.UTF16FromString(value)
	if err != nil {
		return nil
	}
	return encoded
}
