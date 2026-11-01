import { createServer } from 'node:http';
import { createServer as createViteServer } from 'vite';

// Serves the recovery fixture page only. No device proxy, loopback only.
const vite = await createViteServer({
  define: {
    'import.meta.env.VITE_SERVER_IP': JSON.stringify('127.0.0.1'),
    'import.meta.env.VITE_SERVER_PORT': JSON.stringify('4317')
  },
  server: { middlewareMode: true }
});
const server = createServer((req, res) => vite.middlewares(req, res));
server.listen(4317, '127.0.0.1', () =>
  console.info('Browser fixture: http://127.0.0.1:4317/tests/browser/recovery.html')
);
async function close() {
  await vite.close();
  server.close();
}
process.once('SIGTERM', () => void close());
process.once('SIGINT', () => void close());
