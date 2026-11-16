import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  build: { outDir: '../internal/web/ui/dist', emptyOutDir: true },
  // `walroos hub --dev` listens here
  server: { proxy: { '/api': 'http://127.0.0.1:8080' } },
});
