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

// The tray is written directly against user32/shell32 rather than through a
// toolkit. It is one icon and one popup menu; every Go GUI toolkit on Windows
// wants cgo, and this project builds with CGO_ENABLED=0 on purpose: a portable
// drive is copied between machines, and a static binary with nothing to install
// is the only kind that survives that.

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")
)

var (
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procPostThreadMessageW  = user32.NewProc("PostThreadMessageW")
	procGetCurrentThreadID  = kernel32.NewProc("GetCurrentThreadId")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procSetTimer            = user32.NewProc("SetTimer")
	procKillTimer           = user32.NewProc("KillTimer")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procLoadCursorW         = user32.NewProc("LoadCursorW")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procCreateIconFromRes   = user32.NewProc("CreateIconFromResourceEx")
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
	procIsDialogMessageW    = user32.NewProc("IsDialogMessageW")
	procGetDlgItem          = user32.NewProc("GetDlgItem")
	procGetStockObject      = gdi32.NewProc("GetStockObject")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procGlobalAlloc         = kernel32.NewProc("GlobalAlloc")
	procGlobalLock          = kernel32.NewProc("GlobalLock")
	procGlobalUnlock        = kernel32.NewProc("GlobalUnlock")
	procRtlMoveMemory       = kernel32.NewProc("RtlMoveMemory")
	procCoInitializeEx      = ole32.NewProc("CoInitializeEx")
)

const (
	classNameButton = "BUTTON"
	classNameStatic = "STATIC"
	classNameEdit   = "EDIT"
	wndClassTray    = "TeldrivePortableTray"
	wndClassPrompt  = "TeldrivePortablePrompt"

	// Styles. The tray window is never shown: it exists only to receive the
	// notification area's messages.
	wsOverlapped  = 0x00000000
	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsChild       = 0x40000000
	wsVisible     = 0x10000000
	wsTabStop     = 0x00010000
	wsBorder      = 0x00800000
	bsPushButton  = 0x00000000
	ssLeft        = 0x00000000
	esLeft        = 0x0000
	esReadOnly    = 0x0800
	esAutoHScroll = 0x0080
	esPassword    = 0x0020

	wmDestroy     = 0x0002
	wmSetFont     = 0x0030
	wmClose       = 0x0010
	wmCommand     = 0x0111
	wmTimer       = 0x0113
	wmNull        = 0x0000
	wmQuit        = 0x0012
	wmLButtonUp   = 0x0202
	wmLButtonDbl  = 0x0203
	wmRButtonUp   = 0x0205
	wmApp         = 0x8000
	wmAppTray     = wmApp + 1
	defaultGuiObj = 17 // DEFAULT_GUI_FONT

	nimAdd     = 0x00000000
	nimModify  = 0x00000001
	nimDelete  = 0x00000002
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010
	niifInfo   = 0x00000001
	// idiApplication and idcArrow are stock resources; both are
	// MAKEINTRESOURCE values rather than handles to files.
	idiApplication = 32512
	idcArrow       = 32512

	mfString      = 0x00000000
	mfSeparator   = 0x00000800
	tpmReturnCmd  = 0x0100
	tpmRightAlign = 0x0008
	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	mbOK            = 0x00000000
	mbIconError     = 0x00000010
	mbIconInfo      = 0x00000040
	mbSetForeground = 0x00010000

	promptInputID  = 2001
	promptOKID     = 2002
	promptCancelID = 2003

	idMenuOpen = 3101
	idMenuCopy = 3102
	idMenuQuit = 3103
)

const (
	// trayIconID is any value, as long as it is stable for the life of the icon.
	trayIconID = 1
	// timerRefresh is the timer that keeps the tooltip honest.
	timerRefresh = 1
	// refreshIntervalMS is deliberately slow: the numbers barely move, and a
	// program that wakes up continuously just to redraw a tooltip is a bug.
	refreshIntervalMS = 2000
	// maxTooltip is the notification area's limit, in characters.
	maxTooltip = 120
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

// tray is the single live icon. The window procedure is a package function
// because the system calls it, so the state it needs lives here.
type tray struct {
	cb   Callbacks
	hwnd uintptr
	icon uintptr

	lastAddress string
	notified    bool
}

var (
	currentMu   sync.Mutex
	current     *tray
	uiThreadID  uintptr
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

// Run creates the tray icon and pumps messages until the user quits. It blocks,
// and it must be called from the goroutine that owns the main thread, because
// that is where the message queue lives.
func Run(callbacks Callbacks) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Nothing here needs COM today; initialising keeps the door open for the
	// file dialogs the download settings will need, and costs nothing.
	_, _, _ = procCoInitializeEx.Call(0, 2) // COINIT_APARTMENTTHREADED

	thread, _, _ := procGetCurrentThreadID.Call()
	instance, _, _ := procGetModuleHandleW.Call(0)
	app := &tray{cb: callbacks, icon: loadAppIcon()}

	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	class := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(trayProc),
		hInstance:     instance,
		hIcon:         app.icon,
		hCursor:       cursor,
		lpszClassName: utf16Ptr(wndClassTray),
	}
	if _, err := call(procRegisterClassExW, uintptr(unsafe.Pointer(&class))); err != nil {
		return fmt.Errorf("register the tray window: %w", err)
	}

	// The window is never shown. It exists because the notification area sends
	// the icon's clicks to a window, and a message loop needs something to pump.
	hwnd, err := call(procCreateWindowExW,
		0,
		uintptr(unsafe.Pointer(utf16Ptr(wndClassTray))),
		uintptr(unsafe.Pointer(utf16Ptr(callbacks.title()))),
		wsOverlapped,
		0, 0, 0, 0, 0, 0, instance, 0)
	if err != nil || hwnd == 0 {
		return fmt.Errorf("create the tray window: %v", err)
	}
	app.hwnd = hwnd
	currentMu.Lock()
	current = app
	uiThreadID = thread
	currentMu.Unlock()

	if err := app.addIcon(); err != nil {
		procDestroyWindow.Call(hwnd)
		currentMu.Lock()
		current = nil
		currentMu.Unlock()
		return err
	}
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

	app.removeIcon()
	currentMu.Lock()
	current = nil
	uiThreadID = 0
	currentMu.Unlock()
	return nil
}

// Stop ends the message loop, the programmatic form of the tray's quit item. A
// caller that decided to shut down (the server stopped itself, or a test is
// finished) uses this instead of waiting for a click.
func Stop() {
	currentMu.Lock()
	thread := uiThreadID
	currentMu.Unlock()
	if thread != 0 {
		procPostThreadMessageW.Call(thread, wmQuit, 0, 0)
	}
}

func (t *tray) addIcon() error {
	data := t.iconData()
	data.uFlags = nifMessage | nifIcon | nifTip
	copy(data.szTip[:], syscall.StringToUTF16(truncate(t.tooltip(), maxTooltip)))
	added, _, _ := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&data)))
	if added == 0 {
		return errors.New("the notification area did not accept the icon")
	}
	return nil
}

func (t *tray) removeIcon() {
	data := t.iconData()
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
}

func (t *tray) iconData() notifyIconDataW {
	data := notifyIconDataW{
		hWnd:             t.hwnd,
		uID:              trayIconID,
		uCallbackMessage: wmAppTray,
		hIcon:            t.icon,
	}
	// The size is what tells the shell which version of the structure it was
	// handed; letting it read past the fields this program fills would be worse.
	data.cbSize = uint32(unsafe.Sizeof(data))
	return data
}

func (t *tray) status() Status {
	if t.cb.Status == nil {
		return Status{}
	}
	return t.cb.Status()
}

func (t *tray) tooltip() string {
	snapshot := t.status()
	tip := t.cb.title()
	if snapshot.Address != "" {
		tip += " · " + snapshot.Address
	}
	if snapshot.Pending > 0 {
		tip += fmt.Sprintf(" · %d 台待批准", snapshot.Pending)
	}
	if snapshot.Detail != "" && snapshot.Address == "" {
		tip += " · " + snapshot.Detail
	}
	return tip
}

// refresh keeps the tooltip and the "somebody is waiting" balloon current. It
// runs on the UI thread and only reads what the poller prepared.
func (t *tray) refresh() {
	snapshot := t.status()
	data := t.iconData()
	data.uFlags = nifTip
	copy(data.szTip[:], syscall.StringToUTF16(truncate(t.tooltip(), maxTooltip)))
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&data)))
	t.lastAddress = snapshot.Address

	// A device asking for access is the one event worth interrupting for: the
	// whole feature is a person deciding, and a request waits only ten minutes.
	if snapshot.Pending > 0 && !t.notified {
		t.notified = true
		t.balloon("有设备请求访问", fmt.Sprintf("%d 台设备正在等待你的批准。", snapshot.Pending))
	}
	if snapshot.Pending == 0 {
		t.notified = false
	}
}

func (t *tray) balloon(title, message string) {
	data := t.iconData()
	data.uFlags = nifInfo
	copy(data.szInfoTitle[:], syscall.StringToUTF16(title))
	copy(data.szInfo[:], syscall.StringToUTF16(message))
	data.dwInfoFlags = niifInfo
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&data)))
}

// menu is the right-click menu. It is rebuilt each time it is shown, which is
// cheaper than keeping it in sync with the state it displays.
func (t *tray) menu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	appendItem := func(flags, id uintptr, text string) {
		procAppendMenuW.Call(menu, flags, id, uintptr(unsafe.Pointer(utf16Ptr(text))))
	}
	appendItem(mfString, idMenuOpen, "打开网页")
	appendItem(mfString, idMenuCopy, "复制地址")
	appendItem(mfSeparator, 0, "")
	appendItem(mfString, idMenuQuit, "退出 Teldrive")

	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	// Without the foreground hand-off the menu stays up after the click that
	// dismissed it; this is the documented sequence for a tray menu.
	procSetForegroundWindow.Call(t.hwnd)
	command, _, _ := procTrackPopupMenu.Call(menu, tpmReturnCmd|tpmRightAlign,
		uintptr(cursor.x), uintptr(cursor.y), 0, t.hwnd, 0)
	procPostMessageW.Call(t.hwnd, wmNull, 0, 0)
	if command != 0 {
		t.command(int(command))
	}
}

func (t *tray) command(id int) {
	switch id {
	case idMenuOpen:
		t.openBrowser()
	case idMenuCopy:
		t.copyAddress()
	case idMenuQuit:
		t.quit()
	}
}

func (t *tray) openBrowser() {
	if t.cb.OpenBrowser == nil {
		return
	}
	if err := t.cb.OpenBrowser(); err != nil {
		t.cb.log("open browser failed", "error", err)
	}
}

func (t *tray) copyAddress() {
	if t.cb.Copy == nil {
		return
	}
	address := t.status().Address
	if address == "" {
		address = t.lastAddress
	}
	if address == "" {
		return
	}
	if err := t.cb.Copy(address); err != nil {
		t.cb.log("copy address failed", "error", err)
	}
}

func (t *tray) quit() {
	if t.cb.Quit != nil {
		t.cb.Quit()
	}
	procPostQuitMessage.Call(0)
}

func trayProc(hwnd, message, wParam, lParam uintptr) uintptr {
	currentMu.Lock()
	app := current
	currentMu.Unlock()

	switch message {
	case wmTimer:
		if app != nil {
			app.refresh()
		}
		return 0
	case wmAppTray:
		if app != nil {
			switch uint32(lParam) {
			case wmLButtonUp, wmLButtonDbl:
				// A double-click is the same gesture as double-clicking the
				// program: the drive is the page, so open it.
				app.openBrowser()
			case wmRButtonUp:
				app.menu()
			}
		}
		return 0
	case wmClose:
		if app != nil {
			app.quit()
		} else {
			procDestroyWindow.Call(hwnd)
		}
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	result, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return result
}

// Message shows a modal information box.
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
// the tray's own loop.
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
		hbrBackground: 16, // COLOR_BTNFACE + 1, the dialog grey
		lpszClassName: utf16Ptr(wndClassPrompt),
	}
	_, _ = call(procRegisterClassExW, uintptr(unsafe.Pointer(&class)))

	dialog, err := call(procCreateWindowExW,
		0x00000008, // WS_EX_TOPMOST: the prompt must not end up behind a browser
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
		if control != 0 && font != 0 {
			procSendMessageW.Call(control, wmSetFont, font, 1)
		}
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
		switch int(wParam & 0xffff) {
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

// CopyToClipboard puts text on the clipboard, replacing whatever was there.
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
		// The notification area draws at 16 pixels; the smallest image that is
		// not smaller than that is the sharpest one to hand it.
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

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
