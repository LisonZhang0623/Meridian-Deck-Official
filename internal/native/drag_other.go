//go:build !windows

package native

import "errors"

func DragFile(uintptr, string) error {
	return errors.New("本版本仅在 Windows 支持原生拖出，请使用下载另存")
}
