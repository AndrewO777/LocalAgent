import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Vite config for the LocalAgent UI.
//
//   - Dev server on :5173 with HMR. /api and /api/.../events (SSE) are proxied
//     to the Go server on :8080 so you can run `npm run dev` and `go run .`
//     side by side without CORS plumbing.
//   - Production build outputs to web/dist, which the Go binary embeds via
//     //go:embed all:dist (see web/embed.go).
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2022',
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        // SSE: keep the connection open and don't buffer. http-proxy (which
        // Vite uses under the hood) handles this correctly when the upstream
        // sends `Content-Type: text/event-stream`.
        ws: false,
      },
    },
  },
});
