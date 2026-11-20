package main

import (
	"runtime"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"sshtool/internal/core"
	"sshtool/internal/desktop"
)

func showWorkbench(window *application.WebviewWindow) {
	window.UnMinimise()
	window.Show().Focus()
}

func setupWindowClose(app *application.App, window *application.WebviewWindow, hooks desktop.Hooks, traySupported bool) {
	var prompting, quitting atomic.Bool
	requestClose := func(forceQuit bool) {
		if quitting.Load() || !prompting.CompareAndSwap(false, true) {
			return
		}
		go func() {
			defer prompting.Store(false)
			action := hooks.CloseAction()
			if forceQuit {
				action = core.CloseQuit
			} else if action == core.CloseTray && !traySupported {
				action = core.CloseAsk
			}
			if action == core.CloseTray {
				window.Hide()
				return
			}

			dirty := hooks.Dirty()
			if action != core.CloseQuit || dirty {
				choice := ""
				if forceQuit && !window.IsVisible() {
					showWorkbench(window)
				}
				message := "关闭窗口时要执行什么操作？"
				if dirty {
					message += "\n退出将停止传输，未保存的编辑会丢失。"
				}
				application.InvokeSync(func() {
					dialog := app.Dialog.Question().SetTitle("关闭 " + appName).SetMessage(message).AttachToWindow(window)
					dialog.AddButton("取消").SetAsDefault().SetAsCancel()
					if action == core.CloseAsk && traySupported {
						dialog.AddButton("收起到系统托盘").OnClick(func() { choice = core.CloseTray })
					}
					dialog.AddButton("退出程序").OnClick(func() { choice = core.CloseQuit })
					dialog.Show()
				})
				switch choice {
				case core.CloseTray:
					window.Hide()
					return
				case core.CloseQuit:
				default:
					return
				}
			}
			quitting.Store(true)
			app.Quit()
		}()
	}

	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if !quitting.Load() {
			event.Cancel()
			requestClose(false)
		}
	})
	if traySupported {
		tray := app.SystemTray.New()
		tray.SetTooltip(appName)
		if runtime.GOOS == "darwin" {
			tray.SetIcon(appIcon)
		}
		menu := app.NewMenu()
		menu.Add("显示主窗口").OnClick(func(*application.Context) { showWorkbench(window) })
		menu.Add("退出 " + appName).OnClick(func(*application.Context) { requestClose(true) })
		tray.SetMenu(menu)
		tray.OnClick(func() { showWorkbench(window) })
	}
}
