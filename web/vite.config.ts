import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'
import { mockPlugin } from './mock-plugin'

const isMock = process.env.VITE_MOCK === 'true'

export default defineConfig({
  plugins: [react(), ...(isMock ? [mockPlugin()] : [])],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    proxy: isMock
      ? {}
      : {
          '/api': 'http://localhost:8080',
          '/auth': 'http://localhost:8080',
        },
  },
})
