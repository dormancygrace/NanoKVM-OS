import { readFileSync } from 'node:fs';
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig, type Plugin } from 'vite';

// MSW's service worker is only needed with `--mode mocked`. It lives outside
// public/ so that production builds do not ship it; this plugin serves it
// (dev server) or emits it (build) in mocked mode only.
const mswWorker = 'mockServiceWorker.js';
const mswWorkerUrl = new URL(`./msw/${mswWorker}`, import.meta.url);

function mockServiceWorker(): Plugin {
  return {
    name: 'nanokvm-mock-service-worker',
    configureServer(server) {
      server.middlewares.use(`/${mswWorker}`, (_req, res) => {
        res.setHeader('Content-Type', 'text/javascript');
        res.end(readFileSync(mswWorkerUrl));
      });
    },
    generateBundle() {
      this.emitFile({ type: 'asset', fileName: mswWorker, source: readFileSync(mswWorkerUrl) });
    }
  };
}

export default defineConfig(({ mode }) => ({
  plugins: [react(), tailwindcss(), ...(mode === 'mocked' ? [mockServiceWorker()] : [])],
  resolve: {
    tsconfigPaths: true
  },
  server: {
    port: 3001
  }
}));
