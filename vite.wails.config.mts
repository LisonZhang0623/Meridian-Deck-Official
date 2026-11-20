import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'node:path'

export default defineConfig({
  root: resolve('src/renderer'),
  plugins: [vue()],
  resolve: { alias: { '@': resolve('src/renderer/src') } },
  build: { outDir: resolve('frontend/dist'), emptyOutDir: true },
})
