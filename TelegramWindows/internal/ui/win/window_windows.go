//go:build windows

// Package win 提供 Windows 原生窗口宿主。
//
// M0 阶段：纯 Win32（user32/gdi32）画一个状态窗口，零第三方 UI 依赖、零 cgo。
// M1/M2 阶段在同一宿主里嵌入 WebView2 子窗口，web/ 提供前端。
package win

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"syscall"
)

// 窗口消息与样式常量（winuser.h）。
const (
	wsOverlappedWindow = 0x00CF0000
	wsVisible          = 0x10000000

	swShow = 5

	csVRedraw = 0x0001
	csHRedraw = 0x0002

	wmDestroy = 0x0002
	wmPaint   = 0x000F
	wmKeyDown = 0x0100
	wmTimer   = 0x0113

	vkEscape = 0x1B
	idcArrow = 32512

	transparent = 1

	whiteBrush     = 0 // GetStockObject 索引
	defaultGUIFont = 17

	dtLeft       = 0x0000
	dtVCenter    = 0x0004
	dtSingleLine = 0x0020
	dtNoPrefix   = 0x0800

	colorText = 0x00202020 // COLORREF: 0x00BBGGRR，深灰
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")

	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procShowWindow       = user32.NewProc("ShowWindow")
	procUpdateWindow     = user32.NewProc("UpdateWindow")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procBeginPaint       = user32.NewProc("BeginPaint")
	procEndPaint         = user32.NewProc("EndPaint")
	procGetClientRect    = user32.NewProc("GetClientRect")
	procFillRect         = user32.NewProc("FillRect")
	procDrawTextW        = user32.NewProc("DrawTextW")
	procSetTimer         = user32.NewProc("SetTimer")
	procLoadCursorW      = user32.NewProc("LoadCursorW")

	gdiSetTextColor   = gdi32.NewProc("SetTextColor")
	gdiSetBkMode      = gdi32.NewProc("SetBkMode")
	gdiGetStockObject = gdi32.NewProc("GetStockObject")
)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       syscall.Handle
}

type msgT struct {
	hwnd    syscall.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

type rectT struct {
	left, top, right, bottom int32
}

type paintStructT struct {
	hdc         syscall.Handle
	fErase      int32
	rcPaint     rectT
	fRestore    int32
	fIncUpdate  int32
	rgbReserved [32]byte
}

// Config 是窗口参数。
type Config struct {
	Title  string
	Lines  []string
	Width  int32
	Height int32
	// Smoke > 0 时窗口打开后自动退出，用于自动化自检。
	Smoke time.Duration
}

// current 保存当前窗口配置（单窗口应用，M0 简化）。
var current *Config

// Run 创建窗口并进入消息循环，直到窗口关闭。
func Run(cfg Config) error {
	if cfg.Title == "" {
		cfg.Title = "TGW"
	}
	if cfg.Width <= 0 {
		cfg.Width = 960
	}
	if cfg.Height <= 0 {
		cfg.Height = 640
	}
	if len(cfg.Lines) == 0 {
		cfg.Lines = []string{"TGW"}
	}

	// Win32 窗口必须在同一个 OS 线程上创建与抽消息。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	current = &cfg

	hInstance, _, callErr := procGetModuleHandleW.Call(0)
	if hInstance == 0 {
		return fmt.Errorf("win: GetModuleHandle 失败: %v", callErr)
	}

	className, err := syscall.UTF16PtrFromString("TGWMainWindow")
	if err != nil {
		return fmt.Errorf("win: 类名编码失败: %w", err)
	}
	title, err := syscall.UTF16PtrFromString(cfg.Title)
	if err != nil {
		return fmt.Errorf("win: 标题编码失败: %w", err)
	}

	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)

	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		style:         csHRedraw | csVRedraw,
		lpfnWndProc:   syscall.NewCallback(wndProc),
		hInstance:     syscall.Handle(hInstance),
		hCursor:       syscall.Handle(cursor),
		lpszClassName: className,
	}

	if atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		// ERROR_CLASS_ALREADY_EXISTS = 1410，重复运行可接受
		if callErr != syscall.Errno(1410) {
			return fmt.Errorf("win: RegisterClassEx 失败: %v", callErr)
		}
	}

	hwnd, _, callErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow|wsVisible,
		140, 120,
		uintptr(cfg.Width), uintptr(cfg.Height),
		0, 0,
		uintptr(hInstance),
		0,
	)
	if hwnd == 0 {
		return fmt.Errorf("win: CreateWindowEx 失败: %v", callErr)
	}

	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)

	if cfg.Smoke > 0 {
		procSetTimer.Call(hwnd, 1, uintptr(cfg.Smoke.Milliseconds()), 0)
	}

	var msg msgT
	for {
		ret, _, errCall := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		switch int32(ret) {
		case -1:
			return fmt.Errorf("win: GetMessage 失败: %v", errCall)
		case 0:
			return nil // WM_QUIT
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmPaint:
		paint(hwnd)
		return 0
	case wmTimer:
		procPostQuitMessage.Call(0)
		return 0
	case wmKeyDown:
		if wParam == vkEscape {
			procDestroyWindow.Call(hwnd)
		}
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}

func paint(hwnd uintptr) {
	var ps paintStructT
	hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))

	var rc rectT
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))

	brush, _, _ := gdiGetStockObject.Call(whiteBrush)
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), brush)

	gdiSetBkMode.Call(hdc, transparent)
	gdiSetTextColor.Call(hdc, colorText)
	gdiGetStockObject.Call(defaultGUIFont)

	const lineHeight = 28
	y := int32(28)
	for _, line := range current.Lines {
		text, err := syscall.UTF16FromString(line)
		if err != nil || len(text) == 0 {
			continue
		}
		box := rectT{left: 28, top: y, right: rc.right - 28, bottom: y + lineHeight}
		procDrawTextW.Call(
			hdc,
			uintptr(unsafe.Pointer(&text[0])),
			uintptr(len(text)-1),
			uintptr(unsafe.Pointer(&box)),
			dtLeft|dtVCenter|dtSingleLine|dtNoPrefix,
		)
		y += lineHeight
	}
}
