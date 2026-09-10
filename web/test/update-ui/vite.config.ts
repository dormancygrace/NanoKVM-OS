import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';

import base from '../../vite.config.ts';

let operation = {
  state: 'installed',
  id: 'historical-package',
  version: '1.0.0-beta.1',
  message: 'Application update installed'
};
export default defineConfig({
  ...base,
  root: fileURLToPath(new URL('../../', import.meta.url)),
  define: {
    'import.meta.env.VITE_SERVER_IP': JSON.stringify('127.0.0.1'),
    'import.meta.env.VITE_SERVER_PORT': JSON.stringify('18439')
  },
  server: { host: '127.0.0.1', port: 18439, strictPort: true },
  plugins: [
    ...base.plugins!,
    {
      name: 'update-ui-local-api',
      configureServer(server) {
        server.middlewares.use((req, res, next) => {
          const url = req.url?.split('?')[0];
          const send = (data: unknown) => {
            res.setHeader('Content-Type', 'application/json');
            res.end(JSON.stringify({ code: 0, data }));
          };
          if (url === '/__fixture/prepare') {
            operation = {
              state: 'prepared',
              id: 'new-package',
              version: '1.0.0-beta.2',
              message: 'Signature, compatibility and file checks passed'
            };
            return send({});
          }
          if (url === '/__fixture/failed') {
            operation = {
              state: 'failed',
              id: 'new-package',
              version: '1.0.0-beta.2',
              message: 'Test update failed'
            };
            return send({});
          }
          if (url === '/api/os/update/install') {
            operation = {
              ...operation,
              state: 'installed',
              message: 'Application update installed'
            };
            return send({ state: 'installing' });
          }
          if (url === '/api/os/update')
            return send({
              installed: { version: '1.0.0-beta.1', sequence: 3 },
              check: { checking: false, checked_at: '', error: '', release: null },
              operation
            });
          if (url?.startsWith('/api/')) {
            res.statusCode = 404;
            return send({});
          }
          next();
        });
      }
    }
  ]
});
