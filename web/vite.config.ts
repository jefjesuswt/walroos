import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

declare const process: { env: Record<string, string | undefined> }; // no @types/node for one variable

export default defineConfig({
  plugins: [react()],
  build: { outDir: '../internal/web/ui/dist', emptyOutDir: true },
  server: { proxy: { '/api': process.env.WALROOS_API ?? 'http://127.0.0.1:8080' } }, // WALROOS_API: another hub than `walroos hub --dev`
});
