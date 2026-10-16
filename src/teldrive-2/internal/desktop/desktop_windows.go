//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// The shell is written directly against user32/shell32 rather than through a
// toolkit. The whole window is a handful of stock controls and one tray icon,
// and every Go GUI toolkit on Windows wants cgo, which this project builds
// without on purpose: a portable drive is copied between machines, and a static
// binary with no runtime to install is the only kind that survives that.

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")
)

var (
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procUnregisterClassW    = user32.NewProc("UnregisterClassW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procUpdateWindow        = user32.NewProc("UpdateWindow")
	procSetWindowTextW      = user32.NewProc("SetWindowTextW")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procSetTimer            = user32.NewProc("SetTimer")
	procKillTimer           = user32.NewProc("KillTimer")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procLoadCursorW         = user32.NewProc("LoadCursorW")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procCreateIconFromRes   = user32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon         = user32.NewProc("DestroyIcon")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procMessageBoxW         = user32.NewProc("MessageBoxW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procOpenClipboard       = user32.NewProc("OpenClipboard")
	procEmptyClipboard      = user32.NewProc("EmptyClipboard")
	procSetClipboardData    = user32.NewProc("SetClipboardData")
	procCloseClipboard      = user32.NewProc("CloseClipboard")
	procEnableWindow        = user32.NewProc("EnableWindow")
	procIsDialogMessageW    = user32.NewProc("IsDialogMessageW")
	procGetDlgItem          = user32.NewProc("GetDlgItem")

	procGetStockObject = gdi32.NewProc("GetStockObject")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procRtlMoveMemory    = kernel32.NewProc("RtlMoveMemory")

	procCoInitializeEx = ole32.NewProc("CoInitializeEx")
)

const (
	// Control classes, as the window manager calls them.
	classNameButton  = "BUTTON"
	classNameStatic  = "STATIC"
	classNameEdit    = "EDIT"
	wndClassMainName = "TeldrivePortableShell"
	wndClassPrompt   = "TeldrivePortablePrompt"

	// Styles.
	wsOverlapped  = 0x00000000
	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	wsVisible     = 0x10000000
	wsChild       = 0x40000000
	wsTabStop     = 0x00010000
	wsBorder      = 0x00800000
	wsVScroll     = 0x00200000

	bsPushButton   = 0x00000000
	bsAutoCheckBox = 0x00000003
	ssLeft         = 0x00000000
	ssRight        = 0x00000002
	esLeft         = 0x0000
	esReadOnly     = 0x0800
	esAutoHScroll  = 0x0080
	esPassword     = 0x0020
	esMultiline    = 0x0004

	// Window messages.
	wmDestroy     = 0x0002
	wmSize        = 0x0005
	wmSetFont     = 0x0030
	wmClose       = 0x0010
	wmCommand     = 0x0111
	wmTimer       = 0x0113
	wmNull        = 0x0000
	wmLButtonUp   = 0x0202
	wmLButtonDbl  = 0x0203
	wmRButtonUp   = 0x0205
	wmApp         = 0x8000
	wmAppTray     = wmApp + 1
	wmQuit        = 0x0012
	wmKeyDown     = 0x0100
	wmSysCommand  = 0x0112
	scMinimize    = 0xF020
	scRestore     = 0xF120
	swHide        = 0
	swShowNormal  = 1
	swRestore     = 9
	swMinimize    = 6
	defaultGuiObj = 17 // DEFAULT_GUI_FONT

	// Tray icon.
	nimAdd     = 0x00000000
	nimModify  = 0x00000001
	nimDelete  = 0x00000002
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010
	niifInfo   = 0x00000001
	// idiApplication and idcArrow are the stock resources; both are
	// MAKEINTRESOURCE values rather than handles to files.
	idiApplication = 32512
	idcArrow       = 32512

	// Menus.
	mfString      = 0x00000000
	mfSeparator   = 0x00000800
	mfGrayed      = 0x00000001
	tpmReturnCmd  = 0x0100
	tpmRightAlign = 0x0008
	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	// Dialog helpers.
	mbOK            = 0x00000000
	mbIconError     = 0x00000010
	mbIconInfo      = 0x00000040
	mbIconWarning   = 0x00000030
	mbIconQuestion  = 0x00000020
	mbSetForeground = 0x00010000

	promptInputID  = 2001
	promptOKID     = 2002
	promptCancelID = 2003

	idOpenBrowser = 3001
	idCopyAddress = 3002
	idShowWindow  = 3003
	idQuit        = 3004
	idMenuOpen    = 3101
	idMenuCopy    = 3102
	idMenuShow    = 3103
	idMenuQuit    = 3104
)

// IconID is a user-chosen tray icon identity; any value works as long as it is
// stable for the life of the icon.
const trayIconID = 1

const (
	// timerRefresh is how often the labels and the tooltip are recomputed.
	timerRefresh = 1
	// refreshIntervalMS is deliberately slow: the numbers barely move, and a
	// window that redraws continuously is a window that wakes the CPU.
	refreshIntervalMS = 2000
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type point struct{ x, y int32 }

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type notifyIconDataW struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
}

// shell is the single live window. The window procedure is a package function
// because the system calls it, so the state it needs lives here.
type shell struct {
	cb Callbacks

	hwnd uintptr
	icon uintptr

	titleLabel   uintptr
	addressEdit  uintptr
	networkEdit  uintptr
	pendingLabel uintptr
	detailLabel  uintptr
	openButton   uintptr
	copyButton   uintptr
	quitButton   uintptr
	hintLabel    uintptr

	lastAddress string
	notified    bool
}

var (
	currentMu sync.Mutex
	current   *shell

	// promptValue carries the answer out of the modal prompt, whose message loop
	// runs on the UI thread until its window is destroyed.
	promptMu    sync.Mutex
	promptValue string
	promptOK    bool
)

func utf16Ptr(value string) *uint16 {
	pointer, err := syscall.UTF16PtrFromString(value)
	if err != nil {
		// Only possible when the string contains a NUL, which no label here does.
		return nil
	}
	return pointer
}

func call(proc *syscall.LazyProc, args ...uintptr) (uintptr, error) {
	result, _, err := proc.Call(args...)
	if result == 0 {
		if err != nil && !errors.Is(err, syscall.Errno(0)) {
			return 0, err
		}
	}
	return result, nil
}

// Run creates the window and the tray icon and pumps messages until the user
// quits. It blocks, and it must be called from the goroutine that owns the main
// thread, because that is where the message queue lives.
func Run(callbacks Callbacks) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// The shell does not need COM for anything it does today; initialising here
	// keeps the door open for the shell dialogs and costs nothing when it fails.
	_, _, _ = procCoInitializeEx.Call(0, 2) // COINIT_APARTMENTTHREADED

	instance, _, _ := procGetModuleHandleW.Call(0)
	app := &shell{cb: callbacks}

	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	class := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(windowProc),
		hInstance:     instance,
		hIcon:         loadAppIcon(),
		hCursor:       cursor,
		hbrBackground: 16, // COLOR_BTNFACE + 1, the dialog grey
		lpszClassName: utf16Ptr(wndClassMainName),
	}
	if _, err := call(procRegisterClassExW, uintptr(unsafe.Pointer(&class))); err != nil {
		return fmt.Errorf("register the window class: %w", err)
	}
	app.icon = class.hIcon

	// A window started at login stays off screen until the tray icon is used: an
	// unsolicited window on top of somebody's desktop at login is a bug.
	style := uintptr(wsOverlapped | wsCaption | wsSysMenu | wsMinimizeBox)
	if !callbacks.StartHidden {
		style |= wsVisible
	}
	hwnd, err := call(procCreateWindowExW,
		0,
		uintptr(unsafe.Pointer(utf16Ptr(wndClassMainName))),
		uintptr(unsafe.Pointer(utf16Ptr(callbacks.title()))),
		style,
		0x80000000, // CW_USEDEFAULT
		0x80000000,
		560, 330,
		0, 0, instance, 0)
	if err != nil || hwnd == 0 {
		return fmt.Errorf("create the window: %v", err)
	}
	app.hwnd = hwnd
	currentMu.Lock()
	current = app
	currentMu.Unlock()

	app.createControls(instance)
	app.setFonts()
	app.refresh()
	app.addTrayIcon()
	procSetTimer.Call(hwnd, timerRefresh, refreshIntervalMS, 0)

	var message msg
	for {
		result, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}

	app.removeTrayIcon()
	currentMu.Lock()
	current = nil
	currentMu.Unlock()
	return nil
}

func (s *shell) createControls(instance uintptr) {
	create := func(class, text string, style uint32, x, y, width, height int32, id uintptr) uintptr {
		handle, _ := call(procCreateWindowExW,
			0,
			uintptr(unsafe.Pointer(utf16Ptr(class))),
			uintptr(unsafe.Pointer(utf16Ptr(text))),
			uintptr(wsChild|wsVisible|style),
			uintptr(x), uintptr(y), uintptr(width), uintptr(height),
			s.hwnd, id, instance, 0)
		return handle
	}

	s.titleLabel = create(classNameStatic, "Teldrive 正在运行", ssLeft, 20, 18, 500, 22, 0)
	// A read-only edit rather than a label: the address is the one thing on this
	// window somebody may want to select and paste somewhere else.
	s.addressEdit = create(classNameEdit, "", esReadOnly|esAutoHScroll|wsBorder, 20, 56, 500, 24, 0)
	s.networkEdit = create(classNameEdit, "", esReadOnly|esAutoHScroll|wsBorder, 20, 88, 500, 24, 0)
	s.pendingLabel = create(classNameStatic, "等待批准的设备：0", ssLeft, 20, 122, 500, 20, 0)
	s.detailLabel = create(classNameStatic, "", ssLeft, 20, 146, 500, 20, 0)
	s.hintLabel = create(classNameStatic,
		"关闭窗口只会收进托盘；服务会继续运行，直到你点「退出」。", ssLeft, 20, 174, 500, 20, 0)
	s.openButton = create(classNameButton, "打开网页", bsPushButton|wsTabStop, 20, 214, 150, 32, idOpenBrowser)
	s.copyButton = create(classNameButton, "复制地址", bsPushButton|wsTabStop, 186, 214, 150, 32, idCopyAddress)
	s.quitButton = create(classNameButton, "退出", bsPushButton|wsTabStop, 352, 214, 168, 32, idQuit)
}

// setFonts gives every control the stock dialog font; without it the window
// renders in the ancient bitmap system font.
func (s *shell) setFonts() {
	font, _, _ := procGetStockObject.Call(defaultGuiObj)
	if font == 0 {
		return
	}
	for _, control := range []uintptr{
		s.titleLabel, s.addressEdit, s.networkEdit, s.pendingLabel,
		s.detailLabel, s.hintLabel, s.openButton, s.copyButton, s.quitButton,
	} {
		if control != 0 {
			procSendMessageW.Call(control, wmSetFont, font, 1)
		}
	}
}

// refresh recomputes everything the window shows. It runs on the UI thread and
// only reads a snapshot the poller prepared, so it never blocks on the network.
func (s *shell) refresh() {
	snapshot := Snapshot{}
	if s.cb.Snapshot != nil {
		snapshot = s.cb.Snapshot()
	}
	address := snapshot.Address
	if address == "" {
		address = "正在启动…"
	}
	s.setText(s.addressEdit, "本机："+address)
	network := snapshot.NetworkAddress
	if network == "" {
		network = "其他设备：未开启局域网访问"
	} else {
		network = "其他设备：" + network
	}
	s.setText(s.networkEdit, network)
	s.setText(s.pendingLabel, fmt.Sprintf("等待批准的设备：%d", snapshot.Pending))
	s.setText(s.detailLabel, snapshot.Detail)
	s.lastAddress = snapshot.Address
	s.updateTooltip(snapshot)

	// A device asking for access is the one event worth interrupting for: the
	// whole feature is a person deciding, and a request waits only ten minutes.
	if snapshot.Pending > 0 && !s.notified {
		s.notified = true
		s.balloon("有设备请求访问", fmt.Sprintf("%d 台设备正在等待你的批准。", snapshot.Pending))
	}
	if snapshot.Pending == 0 {
		s.notified = false
	}
}

func (s *shell) setText(control uintptr, text string) {
	if control == 0 {
		return
	}
	procSetWindowTextW.Call(control, uintptr(unsafe.Pointer(utf16Ptr(text))))
}

func (s *shell) controlText(control uintptr) string {
	buffer := make([]uint16, 1024)
	length, _, _ := procGetWindowTextW.Call(control, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if length == 0 {
		return ""
	}
	return syscall.UTF16ToString(buffer[:length])
}

func (s *shell) addTrayIcon() {
	data := s.trayData()
	data.uFlags = nifMessage | nifIcon | nifTip
	procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&data)))
}

func (s *shell) removeTrayIcon() {
	data := s.trayData()
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
}

func (s *shell) trayData() notifyIconDataW {
	data := notifyIconDataW{
		hWnd:             s.hwnd,
		uID:              trayIconID,
		uCallbackMessage: wmAppTray,
		hIcon:            s.icon,
	}
	data.cbSize = uint32(unsafe.Sizeof(data))
	return data
}

func (s *shell) updateTooltip(snapshot Snapshot) {
	data := s.trayData()
	data.uFlags = nifTip
	tip := fmt.Sprintf("%s · %d 台待批准", s.cb.title(), snapshot.Pending)
	if snapshot.Address != "" {
		tip = fmt.Sprintf("%s · %s · %d 台待批准", s.cb.title(), snapshot.Address, snapshot.Pending)
	}
	copy(data.szTip[:], syscall.StringToUTF16(tip))
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&data)))
}

func (s *shell) balloon(title, message string) {
	data := s.trayData()
	data.uFlags = nifInfo
	copy(data.szInfoTitle[:], syscall.StringToUTF16(title))
	copy(data.szInfo[:], syscall.StringToUTF16(message))
	data.dwInfoFlags = niifInfo
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&data)))
}

func (s *shell) showWindow() {
	procShowWindow.Call(s.hwnd, swRestore)
	procSetForegroundWindow.Call(s.hwnd)
}

// trayMenu is the right-click menu. It is rebuilt each time it is shown, which
// is cheaper than keeping it in sync with the state it displays.
func (s *shell) trayMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	appendItem := func(flags uintptr, id uintptr, text string) {
		procAppendMenuW.Call(menu, flags, id, uintptr(unsafe.Pointer(utf16Ptr(text))))
	}
	appendItem(mfString, idMenuOpen, "打开网页")
	appendItem(mfString, idMenuCopy, "复制地址")
	appendItem(mfString, idMenuShow, "显示窗口")
	appendItem(mfSeparator, 0, "")
	appendItem(mfString, idMenuQuit, "退出")

	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	// Without the foreground hand-off the menu stays up after the click that
	// dismissed it; this is the documented sequence for a tray menu.
	procSetForegroundWindow.Call(s.hwnd)
	command, _, _ := procTrackPopupMenu.Call(menu, tpmReturnCmd|tpmRightAlign,
		uintptr(cursor.x), uintptr(cursor.y), 0, s.hwnd, 0)
	procPostMessageW.Call(s.hwnd, wmNull, 0, 0)
	if command != 0 {
		s.command(int(command), 0)
	}
}

func (s *shell) command(id int, _ uintptr) {
	switch id {
	case idOpenBrowser, idMenuOpen:
		s.openBrowser()
	case idCopyAddress, idMenuCopy:
		s.copyAddress()
	case idShowWindow, idMenuShow:
		s.showWindow()
	case idQuit, idMenuQuit:
		s.quit()
	}
}

func (s *shell) openBrowser() {
	if s.cb.OpenBrowser == nil {
		return
	}
	if err := s.cb.OpenBrowser(); err != nil {
		s.cb.log("open browser failed", "error", err)
	}
}

func (s *shell) copyAddress() {
	if s.cb.Copy == nil {
		return
	}
	address := s.lastAddress
	if address == "" {
		return
	}
	if err := s.cb.Copy(address); err != nil {
		s.cb.log("copy address failed", "error", err)
	}
}

func (s *shell) quit() {
	if s.cb.Quit != nil {
		s.cb.Quit()
	}
	procDestroyWindow.Call(s.hwnd)
}

func windowProc(hwnd, message, wParam, lParam uintptr) uintptr {
	currentMu.Lock()
	shell := current
	currentMu.Unlock()

	switch message {
	case wmCommand:
		if shell != nil {
			shell.command(int(wParam&0xffff), (wParam>>16)&0xffff)
		}
		return 0
	case wmTimer:
		if shell != nil {
			shell.refresh()
		}
		return 0
	case wmAppTray:
		if shell != nil {
			switch uint32(lParam) {
			case wmLButtonUp, wmLButtonDbl:
				shell.showWindow()
			case wmRButtonUp:
				shell.trayMenu()
			}
		}
		return 0
	case wmClose:
		// Closing the window hides it: the drive keeps running, and the tray
		// icon is how it comes back. Quitting is the button that says so.
		procShowWindow.Call(hwnd, swHide)
		return 0
	case wmDestroy:
		procKillTimer.Call(hwnd, timerRefresh)
		procPostQuitMessage.Call(0)
		return 0
	}
	result, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return result
}

// Message shows a modal information box. It may be called before the shell
// exists, which is why it does not need the window.
func Message(title, message string) {
	procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr(message))),
		uintptr(unsafe.Pointer(utf16Ptr(title))),
		mbOK|mbIconInfo|mbSetForeground)
}

// Error shows a modal error box.
func Error(title, message string) {
	procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr(message))),
		uintptr(unsafe.Pointer(utf16Ptr(title))),
		mbOK|mbIconError|mbSetForeground)
}

// Prompt asks for one line of text, masked when secret, and returns ok=false
// when the user cancels or closes the box. It runs its own modal message loop,
// so it must be called from the goroutine that owns the main thread and before
// (or after) the main window's own loop.
func Prompt(title, message string, secret bool) (string, bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	instance, _, _ := procGetModuleHandleW.Call(0)
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	class := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(promptProc),
		hInstance:     instance,
		hCursor:       cursor,
		hbrBackground: 16,
		lpszClassName: utf16Ptr(wndClassPrompt),
	}
	_, _ = call(procRegisterClassExW, uintptr(unsafe.Pointer(&class)))

	dialog, err := call(procCreateWindowExW,
		0x00000008, // WS_EX_TOPMOST: the passphrase must not end up behind a browser
		uintptr(unsafe.Pointer(utf16Ptr(wndClassPrompt))),
		uintptr(unsafe.Pointer(utf16Ptr(title))),
		wsOverlapped|wsCaption|wsSysMenu|wsVisible,
		0x80000000, 0x80000000, 460, 200,
		0, 0, instance, 0)
	if err != nil || dialog == 0 {
		return "", false
	}

	promptMu.Lock()
	promptValue, promptOK = "", false
	promptMu.Unlock()

	create := func(class, text string, style uint32, x, y, width, height int32, id uintptr) uintptr {
		handle, _ := call(procCreateWindowExW, 0,
			uintptr(unsafe.Pointer(utf16Ptr(class))),
			uintptr(unsafe.Pointer(utf16Ptr(text))),
			uintptr(wsChild|wsVisible|style),
			uintptr(x), uintptr(y), uintptr(width), uintptr(height),
			dialog, id, instance, 0)
		return handle
	}
	label := create(classNameStatic, message, ssLeft, 16, 14, 420, 40, 0)
	editStyle := uint32(esLeft | esAutoHScroll | wsBorder | wsTabStop)
	if secret {
		editStyle |= esPassword
	}
	edit := create(classNameEdit, "", editStyle, 16, 58, 420, 26, promptInputID)
	ok := create(classNameButton, "确定", bsPushButton|wsTabStop, 226, 100, 100, 30, promptOKID)
	cancel := create(classNameButton, "取消", bsPushButton|wsTabStop, 336, 100, 100, 30, promptCancelID)

	font, _, _ := procGetStockObject.Call(defaultGuiObj)
	for _, control := range []uintptr{label, edit, ok, cancel} {
		procSendMessageW.Call(control, wmSetFont, font, 1)
	}
	procSetForegroundWindow.Call(dialog)

	var event msg
	for {
		result, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&event)), 0, 0, 0)
		if int32(result) <= 0 {
			break
		}
		// IsDialogMessageW gives the box the tab order and the Enter key without
		// needing a dialog resource.
		handled, _, _ := procIsDialogMessageW.Call(dialog, uintptr(unsafe.Pointer(&event)))
		if handled != 0 {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&event)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&event)))
	}

	promptMu.Lock()
	defer promptMu.Unlock()
	return promptValue, promptOK
}

func promptProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch message {
	case wmCommand:
		id := int(wParam & 0xffff)
		switch id {
		case promptOKID:
			buffer := make([]uint16, 512)
			length, _, _ := procGetWindowTextW.Call(childHandle(hwnd, promptInputID),
				uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
			promptMu.Lock()
			promptValue = syscall.UTF16ToString(buffer[:length])
			promptOK = true
			promptMu.Unlock()
			procDestroyWindow.Call(hwnd)
		case promptCancelID:
			procDestroyWindow.Call(hwnd)
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	result, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return result
}

func childHandle(parent uintptr, id uintptr) uintptr {
	handle, _, _ := procGetDlgItem.Call(parent, id)
	return handle
}

// Copy puts text on the clipboard, replacing whatever was there.
func CopyToClipboard(text string) error {
	if text == "" {
		return errors.New("nothing to copy")
	}
	opened, _, _ := procOpenClipboard.Call(0)
	if opened == 0 {
		return errors.New("the clipboard is in use")
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()
	encoded, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}
	size := uintptr(len(encoded) * 2)
	handle, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
	if handle == 0 {
		return errors.New("the clipboard allocation failed")
	}
	target, _, _ := procGlobalLock.Call(handle)
	if target == 0 {
		return errors.New("the clipboard allocation could not be locked")
	}
	// The move is done by the system rather than by unsafe.Slice: the destination
	// is a uintptr the allocator handed back, and turning that into a Go pointer
	// is exactly the conversion vet exists to question.
	procRtlMoveMemory.Call(target, uintptr(unsafe.Pointer(&encoded[0])), size)
	procGlobalUnlock.Call(handle)
	if result, _, _ := procSetClipboardData.Call(cfUnicodeText, handle); result == 0 {
		return errors.New("the clipboard rejected the text")
	}
	return nil
}

// loadAppIcon turns the embedded icon file into an HICON. Windows wants the DIB
// out of the .ico container, so the directory at the front is walked here.
func loadAppIcon() uintptr {
	image, err := faviconBytes()
	if err != nil || len(image) < 22 {
		fallback, _, _ := procLoadIconW.Call(0, idiApplication)
		return fallback
	}
	count := int(image[4]) | int(image[5])<<8
	best := -1
	bestSize := 0
	for index := 0; index < count; index++ {
		offset := 6 + index*16
		if offset+16 > len(image) {
			break
		}
		width := int(image[offset])
		if width == 0 {
			width = 256
		}
		// The tray draws at 16 pixels; the smallest image that is not smaller
		// than that is the sharpest one to hand it.
		if width >= 16 && (bestSize == 0 || width < bestSize) {
			best, bestSize = index, width
		}
	}
	if best < 0 {
		best = 0
	}
	offset := 6 + best*16
	if offset+16 > len(image) {
		return 0
	}
	size := int(image[offset+8]) | int(image[offset+9])<<8 | int(image[offset+10])<<16 | int(image[offset+11])<<24
	start := int(image[offset+12]) | int(image[offset+13])<<8 | int(image[offset+14])<<16 | int(image[offset+15])<<24
	if start < 0 || size <= 0 || start+size > len(image) {
		return 0
	}
	handle, _, _ := procCreateIconFromRes.Call(
		uintptr(unsafe.Pointer(&image[start])), uintptr(size), 1, 0x00030000, 0, 0, 0)
	if handle == 0 {
		fallback, _, _ := procLoadIconW.Call(0, idiApplication)
		return fallback
	}
	return handle
}
