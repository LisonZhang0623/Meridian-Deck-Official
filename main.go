package main

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"sshtool/internal/core"
	"sshtool/internal/desktop"
	"sshtool/internal/native"
)

// Keep frontend/README.md tracked so Go tests also compile on a fresh checkout.
// Release builds first compile Vue into frontend/dist, embedded in this binary.
//
//go:embed all:frontend
var assets embed.FS

//go:embed assets/meridian-deck-icon-porcelain-blue.png
var appIcon []byte

const appName = "Meridian Deck"

func startupFailure(message string, err error) {
	log.Printf("%s: %v", message, err)
	native.ShowError(appName+" 启动失败", message)
	os.Exit(1)
}

func main() {
	base, err := os.UserConfigDir()
	if err != nil {
		startupFailure("无法打开本地数据目录，请检查系统设置后重试。", err)
	}
	directory := os.Getenv("SSHTOOL_USER_DATA")
	for _, argument := range os.Args[1:] {
		if strings.HasPrefix(argument, "--user-data-dir=") {
			directory = strings.TrimPrefix(argument, "--user-data-dir=")
		}
	}
	legacy := ""
	if directory == "" {
		directory = filepath.Join(base, "SSHTool")
		legacy = filepath.Join(base, "ssh-workbench")
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		startupFailure("无法打开本地数据目录，请检查路径后重试。", err)
	}
	static, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		startupFailure("应用文件不完整，请重新获取安装包。", fmt.Errorf("运行 npm run build:frontend：%w", err))
	}
	if _, err = fs.Stat(static, "index.html"); err != nil {
		startupFailure("应用文件不完整，请重新获取安装包。", fmt.Errorf("前端资源缺失，运行 npm run build:frontend：%w", err))
	}
	if err = native.CheckWebView2Runtime(); err != nil {
		startupFailure(err.Error(), err)
	}
	var app *application.App
	var window *application.WebviewWindow
	confirm := func(title, message, action string) bool {
		confirmed := false
		application.InvokeSync(func() {
			dialog := app.Dialog.Question().SetTitle(title).SetMessage(message).AttachToWindow(window)
			dialog.AddButton("取消").SetAsDefault().SetAsCancel()
			dialog.AddButton(action).OnClick(func() { confirmed = true })
			dialog.Show()
		})
		return confirmed
	}
	traySupported := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	ui := desktop.UI{
		TraySupported: traySupported,
		OpenEditorWindow: func(token string) {
			application.InvokeSync(func() {
				name := "editor-" + token
				if existing, ok := app.Window.GetByName(name); ok {
					existing.Show()
					existing.Focus()
					return
				}
				editor := app.Window.NewWithOptions(application.WebviewWindowOptions{
					Name: name, Title: "子午线 · 文件预览", Width: 900, Height: 680, MinWidth: 440, MinHeight: 320,
					URL: "/?editor=" + token, Frameless: runtime.GOOS == "windows", DefaultContextMenuDisabled: true,
				})
				editor.RegisterHook(events.Common.WindowClosing, func(_ *application.WindowEvent) {
					window.EmitEvent("editor-closed", token)
				})
			})
		},
		SelectFiles: func() ([]string, error) {
			return app.Dialog.OpenFile().SetTitle("选择上传的文件").CanChooseFiles(true).CanChooseDirectories(false).AttachToWindow(window).PromptForMultipleSelection()
		},
		SelectDirectory: func(title string) (string, error) {
			return app.Dialog.OpenFile().SetTitle(title).CanChooseFiles(false).CanChooseDirectories(true).AttachToWindow(window).PromptForSingleSelection()
		},
		SelectKey: func() (string, error) {
			return app.Dialog.OpenFile().SetTitle("选择 SSH 私钥").CanChooseFiles(true).CanChooseDirectories(false).AttachToWindow(window).PromptForSingleSelection()
		},
		SelectConfig: func() (string, error) {
			return app.Dialog.OpenFile().SetTitle("导入连接与界面偏好").CanChooseFiles(true).CanChooseDirectories(false).AttachToWindow(window).PromptForSingleSelection()
		},
		SaveConfig: func(name string) (string, error) {
			return app.Dialog.SaveFile().SetMessage("导出 " + appName + " 配置").SetFilename(name).AttachToWindow(window).PromptForSingleSelection()
		},
		SaveFile: func(name string) (string, error) {
			return app.Dialog.SaveFile().SetMessage("下载远程文件").SetFilename(name).AttachToWindow(window).PromptForSingleSelection()
		},
		ConfirmOverwrite: func(path string) bool {
			return confirm("确认替换文件", "远程文件已存在，是否替换？\n"+path, "替换")
		},
		ConfirmDeleteTree: func(path string, count int) bool {
			return confirm("确认递归删除", fmt.Sprintf("将永久删除此远程文件夹及其中的 %d 个条目。\n%s", count, path), "永久删除")
		},
		ConfirmDragTransfer: func(direction string, names []string, destination string) bool {
			action := "上传"
			if direction == "download" {
				action = "下载"
			}
			shown := strings.Join(names[:min(len(names), 5)], "\n")
			if len(names) > 5 {
				shown += fmt.Sprintf("\n…以及另外 %d 项", len(names)-5)
			}
			message := fmt.Sprintf("将 %d 个项目%s到：\n%s\n\n来源：\n%s", len(names), action, destination, shown)
			return confirm("确认拖拽"+action, message, action)
		},
		OpenURL: func(url string) error { return app.Browser.OpenURL(url) },
		DragFile: func(file string) error {
			var err error
			application.InvokeSync(func() { err = native.DragFile(uintptr(window.NativeWindow()), file) })
			return err
		},
	}
	cacheRoot := filepath.Join(directory, "cache", "drag-downloads")
	service, hooks, err := desktop.New(directory, legacy, cacheRoot, ui, func(event core.Event) {
		if app != nil {
			app.Event.Emit("app:event", event)
		}
	})
	if err != nil {
		startupFailure("无法加载本地数据。请检查数据目录及其权限后重试。", err)
	}
	defer hooks.Close()
	identity := sha256.Sum256([]byte(strings.ToLower(directory)))
	app = application.New(application.Options{
		Name: appName, Icon: appIcon, Description: "SSH / SFTP 远程工作台", Services: []application.Service{application.NewService(service)},
		Assets:     application.AssetOptions{Handler: application.AssetFileServerFS(static)},
		OnShutdown: hooks.Close,
		Windows:    application.WindowsOptions{WebviewUserDataPath: filepath.Join(directory, "webview")},
		SingleInstance: &application.SingleInstanceOptions{UniqueID: fmt.Sprintf("SSHTool-Wails-%x", identity[:12]), OnSecondInstanceLaunch: func(application.SecondInstanceData) {
			if window != nil {
				showWorkbench(window)
			}
		}},
	})
	if runtime.GOOS == "darwin" {
		// Wails' native View menu otherwise reloads the WebView before it receives the shortcut.
		menu := application.DefaultApplicationMenu()
		view := menu.FindByRole(application.ViewMenu).GetSubmenu()
		view.RemoveMenuItem(view.FindByRole(application.Reload))
		view.RemoveMenuItem(view.FindByRole(application.ForceReload))
		if first := view.ItemAt(0); first != nil && first.IsSeparator() {
			view.RemoveMenuItem(first)
		}
		app.Menu.Set(menu)
	}
	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: appName, Width: 1320, Height: 860, MinWidth: 900, MinHeight: 620,
		Frameless: runtime.GOOS == "windows", EnableFileDrop: true,
		URL:                        fmt.Sprintf("/?startup=%s&theme=%s", hooks.StartupSettings.StartupAnimation, hooks.StartupSettings.Theme),
		DefaultContextMenuDisabled: true, DevToolsEnabled: os.Getenv("SSHTOOL_DEVTOOLS") == "1",
	})
	setupWindowClose(app, window, hooks, traySupported)
	window.OnWindowEvent(events.Common.WindowFilesDropped, func(event *application.WindowEvent) {
		details := event.Context().DropTargetDetails()
		if details == nil || details.ElementID != "sidebar-panel-files" {
			return
		}
		tokens := hooks.Drop(event.Context().DroppedFiles())
		if len(tokens) > 0 {
			window.EmitEvent("files-dropped", tokens)
		}
	})
	if err = app.Run(); err != nil {
		startupFailure("无法打开工作台，请重新启动应用。", err)
	}
}
