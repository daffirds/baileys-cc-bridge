import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The build is embedded in the Go binary (internal/webui/dist). `npm run dev` proxies the API to a
// running `bailey-cc-bridge run` on its default port.
export default defineConfig({
  plugins: [react()],
  build: { outDir: '../internal/webui/dist', emptyOutDir: true },
  server: { proxy: { '/api': 'http://127.0.0.1:7468' } },
});
