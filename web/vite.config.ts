import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

const bridge = 'http://127.0.0.1:9999';

// The build is embedded in the Go binary (internal/webui/dist). `npm run dev` proxies the API to a
// running `baileys-cc-bridge run` on its default port, presenting the calls as the panel's own: it
// refuses cross-origin ones. Open the dev server with the panel's #t=… token.
export default defineConfig({
  plugins: [react()],
  build: { outDir: '../internal/webui/dist', emptyOutDir: true },
  server: { proxy: { '/api': { target: bridge, changeOrigin: true, headers: { origin: bridge } } } },
});
