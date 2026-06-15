import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Vite 配置
// 开发时通过 vite proxy 转发 /api 到后端 8088
export default defineConfig({
  plugins: [react()],
  server: {
    host: '0.0.0.0',
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8088',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
    chunkSizeWarningLimit: 1500,
  },
})