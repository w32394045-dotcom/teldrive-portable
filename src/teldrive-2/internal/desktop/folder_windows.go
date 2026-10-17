//go:build windows

package desktop

import (
	"syscall"
	"unsafe"
)

// ChooseFolder asks the person at this computer for a folder, starting at
// initial when it exists. It blocks until they answer or cancel.
//
// It exists because the drive is a web page: a browser cannot hand a page a path
// on the machine it runs on, so the page asks the server, and the server - which
// is this process - asks the person.
func ChooseFolder(title, initial string) (string, bool) {
	// The folder dialog is a shell object, and a shell object wants COM.
	// RPC_E_CHANGED_MODE (a thread already initialised with another model) is
	// not a reason to give up; the dialog still works.
	_, _, _ = procCoInitializeEx.Call(0, 2) // COINIT_APARTMENTTHREADED
	defer procCoUninitialize.Call()

	var root uintptr
	if initial != "" {
		var pidl uintptr
		if hr, _, _ := procSHParseDisplayName.Call(
			uintptr(unsafe.Pointer(utf16Ptr(initial))), 0,
			uintptr(unsafe.Pointer(&pidl)), 0, 0); hr == 0 && pidl != 0 {
			root = pidl
			defer procCoTaskMemFree.Call(root)
		}
	}

	buffer := make([]uint16, 1024)
	info := browseInfoW{
		pidlRoot:       root,
		ulFlags:        0x00000001 | 0x00000040 | 0x00000010, // FS dirs, new style, edit box
		pszDisplayName: &buffer[0],
		lpszTitle:      utf16Ptr(title),
	}
	pidl, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&info)))
	if pidl == 0 {
		return "", false
	}
	defer procCoTaskMemFree.Call(pidl)

	if resolved, _, _ := procSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&buffer[0]))); resolved == 0 {
		return "", false
	}
	path := syscall.UTF16ToString(buffer)
	if path == "" {
		return "", false
	}
	return path, true
}

type browseInfoW struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

var (
	procSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	procSHParseDisplayName   = shell32.NewProc("SHParseDisplayName")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
	procCoUninitialize       = ole32.NewProc("CoUninitialize")
)
