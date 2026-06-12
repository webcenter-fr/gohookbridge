import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: '../gohookbridge/web/static',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': 'http://localhost:3333',
      '/events': 'http://localhost:3333',
      '/auth': 'http://localhost:3333',
      '/login': 'http://localhost:3333',
      '/logout': 'http://localhost:3333',
    },
  },
})