import path from "node:path"
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// The build output is embedded into the onebox Go binary via go:embed
// (see internal/webui/app/app.go) and served under /app/ — base must match
// that mount point so built asset URLs resolve correctly.
export default defineConfig({
  base: '/app/',
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(import.meta.dirname, "./src"),
    },
  },
  build: {
    outDir: path.resolve(import.meta.dirname, "../internal/webui/app/dist"),
    emptyOutDir: true,
  },
  server: {
    // Dev server proxies API calls straight to the Go backend (run
    // separately: `go run ./cmd/onebox`) so `npm run dev` never needs its
    // own copy of backend logic.
    proxy: {
      '/api': 'http://localhost:8090',
      '/chat': 'http://localhost:8090',
    },
  },
})
