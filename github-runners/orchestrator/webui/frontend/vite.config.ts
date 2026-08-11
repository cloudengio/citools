import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// base './' keeps asset URLs relative so the built SPA works when embedded and
// served from the Go binary's root. During `npm run dev`, /api is proxied to the
// orchestrator's web UI listen address.
export default defineConfig({
  plugins: [react()],
  base: './',
  build: { outDir: 'dist', emptyOutDir: true },
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8088',
    },
  },
})
