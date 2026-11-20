//go:build windows

package native

import (
	"errors"
	"strconv"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// CheckWebView2Runtime checks the Evergreen registration before Wails starts
// its window. This lets the portable executable show installation instructions.
func CheckWebView2Runtime() error {
	const runtimeKey = `Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		key, err := registry.OpenKey(root, runtimeKey, registry.QUERY_VALUE|registry.WOW64_32KEY)
		if err != nil {
			continue
		}
		version, _, err := key.GetStringValue("pv")
		key.Close()
		if err == nil && registeredWebView2Version(version) {
			return nil
		}
	}
	return errors.New("未检测到 Microsoft Edge WebView2 Evergreen Runtime。请从 Microsoft 官方页面安装运行时后重新启动 Meridian Deck。\n\nhttps://developer.microsoft.com/microsoft-edge/webview2/\n\n离线安装请选择 x64 Evergreen Standalone Installer")
}

func registeredWebView2Version(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 4 {
		return false
	}
	nonzero := false
	for _, part := range parts {
		number, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return false
		}
		nonzero = nonzero || number != 0
	}
	return nonzero
}
