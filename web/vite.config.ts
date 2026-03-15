import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/api': process.env.BACKEND_URL ?? 'http://localhost:8080',
      '/auth': process.env.BACKEND_URL ?? 'http://localhost:8080',
    },
  },
})
