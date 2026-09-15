//go:build windows && amd64

package win32

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

type Rect struct {
	Left   int
	Top    int
	Right  int
	Bottom int
}

func (r Rect) Width() int  { return r.Right - r.Left }
func (r Rect) Height() int { return r.Bottom - r.Top }

type Image struct {
	Width  int
	Height int
	// Pixels are tightly packed BGRA8 pixels in top-down row order.
	Pixels []byte
}

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procGetDC                     = user32.NewProc("GetDC")
	procReleaseDC                 = user32.NewProc("ReleaseDC")
	procCreateCompatibleDC        = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC                  = gdi32.NewProc("DeleteDC")
	procCreateDIBSection          = gdi32.NewProc("CreateDIBSection")
	procSelectObject              = gdi32.NewProc("SelectObject")
	procDeleteObject              = gdi32.NewProc("DeleteObject")
	procBitBlt                    = gdi32.NewProc("BitBlt")
	procSetProcessDPIAware        = user32.NewProc("SetProcessDPIAware")
	procSetProcessDPIAwarenessCtx = user32.NewProc("SetProcessDpiAwarenessContext")
	procMonitorFromWindow         = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfo            = user32.NewProc("GetMonitorInfoW")
	procGetForegroundWindow       = user32.NewProc("GetForegroundWindow")
	procGetWindowTextLength       = user32.NewProc("GetWindowTextLengthW")
	procGetWindowText             = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcessID  = user32.NewProc("GetWindowThreadProcessId")
	procEnumWindows               = user32.NewProc("EnumWindows")
	procIsWindowVisible           = user32.NewProc("IsWindowVisible")
	procOpenProcess               = kernel32.NewProc("OpenProcess")
	procCloseHandle               = kernel32.NewProc("CloseHandle")
	procQueryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
)

const (
	biRGB        = 0
	dibRGBColors = 0
	srccopy      = 0x00CC0020
	captureBlt   = 0x40000000

	monitorDefaultToNearest = 2
	processQueryLimited     = 0x1000

	// Per-monitor-v2 DPI awareness context.
	dpiAwarenessContextPerMonitorV2 = ^uintptr(3)
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]struct {
		Blue     byte
		Green    byte
		Red      byte
		Reserved byte
	}
}

type nativeRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type monitorInfo struct {
	CbSize    uint32
	RcMonitor nativeRect
	RcWork    nativeRect
	Flags     uint32
}

func SetDPIAware() {
	if ret, _, _ := procSetProcessDPIAwarenessCtx.Call(dpiAwarenessContextPerMonitorV2); ret != 0 {
		return
	}
	_, _, _ = procSetProcessDPIAware.Call()
}

func CaptureRect(rect Rect) (*Image, error) {
	width, height := rect.Width(), rect.Height()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid capture rectangle: %+v", rect)
	}

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return nil, lastError("GetDC")
	}
	defer procReleaseDC.Call(0, screenDC)

	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return nil, lastError("CreateCompatibleDC")
	}
	defer procDeleteDC.Call(memDC)

	info := bitmapInfo{Header: bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       int32(width),
		Height:      -int32(height),
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}}
	var bits unsafe.Pointer
	hBitmap, _, _ := procCreateDIBSection.Call(
		screenDC,
		uintptr(unsafe.Pointer(&info)),
		dibRGBColors,
		uintptr(unsafe.Pointer(&bits)),
		0,
		0,
	)
	if hBitmap == 0 || bits == nil {
		return nil, lastError("CreateDIBSection")
	}
	defer procDeleteObject.Call(hBitmap)

	previous, _, _ := procSelectObject.Call(memDC, hBitmap)
	if previous == 0 {
		return nil, lastError("SelectObject")
	}
	defer procSelectObject.Call(memDC, previous)

	ret, _, _ := procBitBlt.Call(
		memDC, 0, 0, uintptr(width), uintptr(height), screenDC,
		uintptr(int32(rect.Left)), uintptr(int32(rect.Top)), srccopy|captureBlt,
	)
	if ret == 0 {
		return nil, lastError("BitBlt")
	}

	size := width * height * 4
	pixels := make([]byte, size)
	copy(pixels, unsafe.Slice((*byte)(bits), size))
	return &Image{Width: width, Height: height, Pixels: pixels}, nil
}

func ForegroundWindow() uintptr {
	hwnd, _, _ := procGetForegroundWindow.Call()
	return hwnd
}

func WindowTitle(hwnd uintptr) string {
	if hwnd == 0 {
		return ""
	}
	length, _, _ := procGetWindowTextLength.Call(hwnd)
	if length == 0 {
		return ""
	}
	buffer := make([]uint16, length+1)
	procGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), length+1)
	return syscall.UTF16ToString(buffer)
}

func WindowProcessPath(hwnd uintptr) string {
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	process, _, _ := procOpenProcess.Call(processQueryLimited, 0, uintptr(pid))
	if process == 0 {
		return ""
	}
	defer procCloseHandle.Call(process)

	buffer := make([]uint16, 1024)
	length := uint32(len(buffer))
	ret, _, _ := procQueryFullProcessImageName.Call(
		process, 0, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&length)),
	)
	if ret == 0 {
		return ""
	}
	return syscall.UTF16ToString(buffer[:length])
}

func IsWardogsWindow(hwnd uintptr, boundProcess string) bool {
	if hwnd == 0 {
		return false
	}
	if boundProcess != "" {
		return strings.EqualFold(WindowProcessPath(hwnd), boundProcess)
	}
	return strings.Contains(strings.ToLower(WindowTitle(hwnd)), "wardogs")
}

// FindWardogsWindow locates the configured game window even while the control
// panel is in the foreground. Without a saved process it uses the window title
// and excludes this utility's own windows.
func FindWardogsWindow(boundProcess string) uintptr {
	var found uintptr
	callback := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
		}
		if boundProcess != "" {
			if strings.EqualFold(WindowProcessPath(hwnd), boundProcess) {
				found = hwnd
				return 0
			}
			return 1
		}
		title := strings.ToLower(WindowTitle(hwnd))
		if strings.Contains(title, "wardogs") && !strings.Contains(title, "mortar") && !strings.Contains(title, "迫击炮") {
			found = hwnd
			return 0
		}
		return 1
	})
	procEnumWindows.Call(callback, 0)
	return found
}

func MonitorRect(hwnd uintptr) Rect {
	monitor, _, _ := procMonitorFromWindow.Call(hwnd, monitorDefaultToNearest)
	if monitor == 0 {
		return Rect{Right: getSystemMetric(0), Bottom: getSystemMetric(1)}
	}
	info := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
	ret, _, _ := procGetMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info)))
	if ret == 0 {
		return Rect{Right: getSystemMetric(0), Bottom: getSystemMetric(1)}
	}
	return Rect{
		Left: int(info.RcMonitor.Left), Top: int(info.RcMonitor.Top),
		Right: int(info.RcMonitor.Right), Bottom: int(info.RcMonitor.Bottom),
	}
}

func getSystemMetric(index uintptr) int {
	proc := user32.NewProc("GetSystemMetrics")
	value, _, _ := proc.Call(index)
	return int(int32(value))
}

func lastError(operation string) error {
	err := syscall.GetLastError()
	if err == nil {
		return fmt.Errorf("%s failed", operation)
	}
	return fmt.Errorf("%s failed: %w", operation, err)
}
