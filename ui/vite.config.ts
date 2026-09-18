/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// The daemon's port. `make dev` exports ADVISOR_PORT so the dev server
// proxies /api to the right place; the default matches server.DefaultPort.
const daemonPort = Number(process.env.ADVISOR_PORT ?? 27182)

export default defineConfig({
  plugins: [react()],
  build: {
    // The Go server embeds this directory (internal/server/ui.go). Nothing
    // else reads it; `make ui` produces it, .gitignore ignores it.
    outDir: '../internal/server/ui/dist',
    emptyOutDir: true,
  },
  server: {
    // Dev only. The daemon binds to 127.0.0.1 and checks the Host header, so
    // the proxy rewrites Host to the daemon's own address (changeOrigin).
    host: '127.0.0.1',
    port: 5173,
    strictPort: false,
    proxy: {
      '/api': {
        target: `http://127.0.0.1:${daemonPort}`,
        changeOrigin: true,
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: false,
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
  },
})
