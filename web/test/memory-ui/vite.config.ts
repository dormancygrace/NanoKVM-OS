import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';

import base from '../../vite.config.ts';

const initial = () => ({
  totalBytes: 193680 * 1024,
  availableBytes: 143984 * 1024,
  usedBytes: (193680 - 143984) * 1024,
  cachedBytes: 20 * 1048576,
  swapTotalBytes: 0,
  swapUsedBytes: 0,
  videoBytes: 48 * 1048576,
  zram: {
    enabled: false,
    available: true,
    sizeMiB: 64,
    usedBytes: 0,
    priority: 100,
    memoryBytes: 0,
    compressedBytes: 0,
    originalBytes: 0,
    algorithm: 'lz4',
    recompress: false,
    recompressAvailable: true,
    recompressReady: false
  },
  sd: { enabled: false, available: true, sizeMiB: 256, usedBytes: 0, priority: 10 }
});
let state = initial();
let mode = 'healthy';
let limit = { enabled: false, limit: 75 };
let events: unknown[] = [];
export default defineConfig({
  ...base,
  root: fileURLToPath(new URL('../../', import.meta.url)),
  define: {
    'import.meta.env.VITE_SERVER_IP': JSON.stringify('127.0.0.1'),
    'import.meta.env.VITE_SERVER_PORT': JSON.stringify('18434')
  },
  server: { host: '127.0.0.1', port: 18434, strictPort: true },
  plugins: [
    ...base.plugins!,
    {
      name: 'memory-local-api',
      configureServer(server) {
        server.middlewares.use(async (req, res, next) => {
          const url = req.url?.split('?')[0];
          if (!url?.startsWith('/api/') && !url?.startsWith('/__fixture/')) return next();
          let body = '';
          for await (const chunk of req) body += chunk;
          const request = body ? JSON.parse(body) : {};
          const send = (data: unknown, code = 0, msg = '') => {
            res.setHeader('Content-Type', 'application/json');
            res.end(JSON.stringify({ code, msg, data }));
          };
          if (url === '/__fixture/config') {
            mode = request.mode;
            if (mode === 'healthy' || mode === 'slow' || mode === 'unavailable') {
              state = initial();
              limit = { enabled: false, limit: 75 };
              events = [];
            }
            if (mode === 'unavailable') {
              state.zram.available = false;
              state.zram.recompressAvailable = false;
              state.zram.recompress = true;
            }
            return send({ mode });
          }
          if (url === '/__fixture/log') return send(events);
          events.push({ method: req.method, url, body: request });
          if (url === '/api/vm/memory/status') {
            const snapshot = structuredClone(state);
            if (mode === 'read-failure') return send(null, -1, 'Fixture read failed');
            if (mode === 'slow') await new Promise((resolve) => setTimeout(resolve, 4500));
            return send(snapshot);
          }
          if (url === '/api/vm/memory/swap' && req.method === 'POST') {
            if (mode === 'write-failure') return send(null, -1, 'Fixture change rejected');
            const item = request.kind === 'zram' ? state.zram : state.sd;
            item.enabled = request.enabled;
            item.sizeMiB = request.sizeMiB;
            if (request.recompress !== undefined) state.zram.recompress = request.recompress;
            state.zram.recompressReady = state.zram.enabled && state.zram.recompress;
            return send(state);
          }
          if (url === '/api/vm/memory/limit') {
            if (req.method === 'POST') limit = request;
            return send(limit);
          }
          res.statusCode = 404;
          return send(null, -1, 'No fixture for this endpoint');
        });
      }
    }
  ]
});
