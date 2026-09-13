import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    tsconfigPaths: true
  },
  server: {
    port: 3001
  },
  build: {
    chunkSizeWarningLimit: 1024
  }
});
