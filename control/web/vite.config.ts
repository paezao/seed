import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';

declare const process: { env: Record<string, string | undefined> };

// Dev only: the kernel embeds its control token in the page it serves; when
// developing against a running kernel, pass it via SEED_TOKEN
// (cat <seed>/.seed/control-token) and SEED_KERNEL=http://127.0.0.1:<port>.
const devToken = (): Plugin => ({
  name: 'seed-dev-token',
  apply: 'serve',
  transformIndexHtml: (html) =>
    process.env.SEED_TOKEN ? html.replace('<head>', `<head><meta name="seed-token" content="${process.env.SEED_TOKEN}">`) : html,
});

const kernel = process.env.SEED_KERNEL ?? 'http://127.0.0.1:8080';

export default defineConfig({
  base: '/_seed/',
  plugins: [react(), devToken()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
    assetsInlineLimit: 0,
    chunkSizeWarningLimit: 600,
  },
  server: {
    proxy: {
      '/_seed/api': {
        target: kernel,
        changeOrigin: true,
        // The kernel refuses cross-origin writes; present proxied requests as its own origin.
        headers: { origin: kernel },
      },
    },
  },
});
