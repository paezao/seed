import { defineConfig } from 'vite';
declare const process: { env: Record<string, string | undefined> };
import react from '@vitejs/plugin-react';

export default defineConfig({
  base: '/_seed/',
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
    assetsInlineLimit: 0,
    chunkSizeWarningLimit: 600,
  },
  server: {
    proxy: {
      '/_seed/api': { target: process.env.SEED_KERNEL ?? 'http://127.0.0.1:8080', changeOrigin: true },
    },
  },
});
