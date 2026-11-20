import { createApp, watch } from 'vue'
import App from './App.vue'
import { state } from './workbench'
import { pickStartupAnimation } from '../../shared/settings'
import './tokens.css'
import './style.css'
import './sidebar.css'
import './gpu.css'
import './file-actions.css'
import './typography.css'
import { installWailsBridge } from './wails'
import { protectWorkspaceFromReload } from './workspace-navigation'

protectWorkspaceFromReload(window)
const useNativeZoom = !window.sshTool
const launch = new URLSearchParams(location.search)
const editorToken = launch.get('editor')
void installWailsBridge(!!editorToken).then(async () => {
  const appRoot = document.getElementById('app')!
  const nativeWindow = useNativeZoom ? (await import('@wailsio/runtime')).Window : undefined
  let zoomUpdate = Promise.resolve()
  watch(() => state.settings.uiZoom, percent => {
    const scale = percent / 100
    if (!nativeWindow) {
      appRoot.style.setProperty('--ui-scale', String(scale))
      return
    }
    zoomUpdate = zoomUpdate.then(async () => {
      try {
        await nativeWindow.SetZoom(scale)
        appRoot.style.removeProperty('--ui-scale')
      } catch (error) {
        appRoot.style.setProperty('--ui-scale', String(scale))
        console.warn('工作台改用页面缩放', error)
      }
    })
  }, { immediate: true })
  const captionWindow = /Windows/i.test(navigator.userAgent) ? nativeWindow : undefined
  if (editorToken) {
    const { default: DetachedEditor } = await import('./components/DetachedEditor.vue')
    createApp(DetachedEditor, { token: editorToken, captionWindow }).mount(appRoot)
  } else {
    const startupAnimation = pickStartupAnimation(launch.get('startup'))
    const theme = launch.get('theme')
    if (theme === 'light' || theme === 'dark') state.settings.theme = theme
    createApp(App, { startupAnimation, captionWindow }).mount(appRoot)
  }
}).catch(error => {
  console.error('工作台启动失败', error)
  document.getElementById('app')!.textContent = '无法打开工作台，请重新启动应用。'
})
