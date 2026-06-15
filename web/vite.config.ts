import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: '../gohookbridge/web/static',
    emptyOutDir: true,
  },
  server: {
    allowedHosts: true,
    port: 8080,
    proxy: {
      '/api': 'http://localhost:8081',
      '/events': 'http://localhost:8081',
      '/auth': 'http://localhost:8081',
      '/login': 'http://localhost:8081',
      '/logout': 'http://localhost:8081',
    },
  },
})