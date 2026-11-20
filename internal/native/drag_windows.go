//go:build windows

package native

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var shell32 = windows.NewLazySystemDLL("shell32.dll")
var ole32 = windows.NewLazySystemDLL("ole32.dll")
var iidDataObject = windows.GUID{Data1: 0x0000010e, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}

type shellDataObject struct{ vtable *[3]uintptr }

var leftButtonHeld = func() bool {
	state, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("GetAsyncKeyState").Call(1)
	return state&0x8000 != 0
}

// DragFile must run on the application's STA/UI thread. Shell-owned IDataObject
// and IDropSource provide CF_HDROP, file and folder icons, Explorer copy and Escape cancel.
// The cache stays owned by SSHTool: only DROPEFFECT_COPY is offered.
func DragFile(hwnd uintptr, file string) error {
	// WebView -> Go calls are asynchronous. A quick mouse-up can arrive before
	// this request; starting Shell drag with no held button can latch its loop.
	if !leftButtonHeld() {
		return nil
	}
	info, err := os.Lstat(file)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() && info.Mode().Type() != os.ModeDir {
		return fmt.Errorf("只能拖出已准备的普通文件或文件夹")
	}
	hr, _, _ := ole32.NewProc("OleInitialize").Call(0)
	if int32(hr) < 0 {
		return fmt.Errorf("初始化原生拖拽失败：0x%08x", uint32(hr))
	}
	defer ole32.NewProc("OleUninitialize").Call()
	parse := func(path string) (uintptr, error) {
		name, e := windows.UTF16PtrFromString(path)
		if e != nil {
			return 0, e
		}
		var pidl uintptr
		hr, _, _ := shell32.NewProc("SHParseDisplayName").Call(uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&pidl)), 0, 0)
		runtime.KeepAlive(name)
		if int32(hr) < 0 {
			return 0, fmt.Errorf("无法准备拖拽文件：0x%08x", uint32(hr))
		}
		return pidl, nil
	}
	parent, err := parse(filepath.Dir(file))
	if err != nil {
		return err
	}
	defer ole32.NewProc("CoTaskMemFree").Call(parent)
	full, err := parse(file)
	if err != nil {
		return err
	}
	defer ole32.NewProc("CoTaskMemFree").Call(full)
	child, _, _ := shell32.NewProc("ILFindLastID").Call(full)
	var object *shellDataObject
	hr, _, _ = shell32.NewProc("SHCreateDataObject").Call(parent, 1, uintptr(unsafe.Pointer(&child)), 0, uintptr(unsafe.Pointer(&iidDataObject)), uintptr(unsafe.Pointer(&object)))
	if int32(hr) < 0 || object == nil {
		return fmt.Errorf("创建拖拽对象失败：0x%08x", uint32(hr))
	}
	defer func() {
		syscall.SyscallN(object.vtable[2], uintptr(unsafe.Pointer(object)))
	}()
	var effect uint32
	if !leftButtonHeld() {
		return nil
	}
	hr, _, _ = shell32.NewProc("SHDoDragDrop").Call(hwnd, uintptr(unsafe.Pointer(object)), 0, 1, uintptr(unsafe.Pointer(&effect)))
	if int32(hr) < 0 {
		return fmt.Errorf("原生拖拽失败：0x%08x", uint32(hr))
	}
	return nil
}
