import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: {
    // NOTE: outDir 在项目根之外，必须显式 emptyOutDir，否则 Vite 只警告、不清空旧产物。
    outDir: '../cmd/server/dist/app',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      // NOTE: 房间 WebSocket 用 location.host 拼地址，所以代理必须开 ws。
      '/api': { target: 'http://localhost:8080', ws: true },
    },
  },
  test: {
    include: ['src/**/*.test.ts'],
  },
})
