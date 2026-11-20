import { defineConfig } from 'electron-vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'node:path'

export default defineConfig({
  main: { logLevel: 'warn', build: { externalizeDeps: true } },
  preload: { logLevel: 'warn', build: { externalizeDeps: true } },
  renderer: {
    logLevel: 'warn',
    plugins: [vue()],
    resolve: { alias: { '@': resolve('src/renderer/src') } }
  }
})
