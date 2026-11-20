//go:build windows

package native

import (
	"golang.org/x/sys/windows"
	"strings"
	"unsafe"
)

func ShowError(title, message string) {
	t, _ := windows.UTF16PtrFromString(strings.ReplaceAll(title, "\x00", ""))
	m, _ := windows.UTF16PtrFromString(strings.ReplaceAll(message, "\x00", ""))
	windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x10)
}
