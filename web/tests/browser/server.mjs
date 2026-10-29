import { createServer } from 'node:http';
import { createServer as createViteServer } from 'vite';
import { WebSocketServer } from 'ws';

// No device proxy and no credentials from the environment. Only loopback.
const state = { loggedIn: false, connections: 0, attempts: 0, reports: [], loginEncoding: '' };
const sockets = new Set();
const server = createServer(async (req, res) => {
  const pathname = new URL(req.url, 'http://localhost').pathname;
  const json = (data, status = 200) => {
    res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
    res.end(JSON.stringify(data));
  };
  const ok = (data) => json({ code: 0, msg: 'success', data });
  if (pathname === '/__test/state') return json(state);
  if (pathname.startsWith('/__test/')) {
    if (req.method !== 'POST') return json({ error: 'POST required' }, 405);
    if (pathname === '/__test/reset') {
      Object.assign(state, {
        loggedIn: false,
        connections: 0,
        attempts: 0,
        reports: [],
        loginEncoding: ''
      });
      for (const socket of sockets) socket.close();
    } else if (pathname === '/__test/reports/reset') state.reports = [];
    else if (pathname === '/__test/drop') {
      for (const socket of sockets) socket.close(1012, 'fixture restart');
    } else if (pathname === '/__test/expire') {
      state.loggedIn = false;
      for (const socket of sockets) socket.close(4401, 'fixture expiry');
    } else return json({ error: 'Unknown test endpoint' }, 404);
    return ok(null);
  }
  if (pathname === '/api/branding')
    return ok({
      title: '',
      logoRevision: '',
      faviconRevision: '',
      customLogoAvailable: false,
      customFaviconAvailable: false,
      bannerStyle: 'default'
    });
  if (pathname === '/api/auth/account') {
    if (!state.loggedIn || !req.headers.cookie?.includes('fixture-session=1'))
      return json({ code: -1, msg: 'unauthorized' }, 401);
    return ok({ username: 'admin', role: 'admin', mustChangePassword: false });
  }
  if (pathname === '/api/auth/login' && req.method === 'POST') {
    let raw = '';
    for await (const chunk of req) {
      raw += chunk;
      if (raw.length > 4096) return json({ error: 'too large' }, 413);
    }
    let body;
    try {
      body = JSON.parse(raw);
    } catch {
      return json({ error: 'bad JSON' }, 400);
    }
    state.loginEncoding = body.passwordEncoding;
    if (
      body.username !== 'admin' ||
      body.password !== 'browser-test-password' ||
      body.passwordEncoding !== 'plain'
    )
      return json({ code: -2, msg: 'invalid credentials', data: null });
    state.loggedIn = true;
    res.setHeader('Set-Cookie', 'fixture-session=1; HttpOnly; SameSite=Strict; Path=/');
    return ok(null);
  }
  if (pathname.startsWith('/api/')) return json({ error: 'No fixture for ' + pathname }, 404);
  vite.middlewares(req, res);
});
const wss = new WebSocketServer({ noServer: true });
server.on('upgrade', (request, socket, head) => {
  if (new URL(request.url, 'http://localhost').pathname !== '/api/ws') return;
  wss.handleUpgrade(request, socket, head, (ws) => {
    state.attempts++;
    if (!state.loggedIn || !request.headers.cookie?.includes('fixture-session=1')) {
      ws.close(4401);
      return;
    }
    sockets.add(ws);
    state.connections++;
    ws.send(
      JSON.stringify({
        type: 'control',
        data: JSON.stringify({ enabled: true, lease: 'fixture-only' })
      })
    );
    ws.on('close', () => sockets.delete(ws));
    ws.on('message', (data, binary) => {
      const bytes = binary ? [...data] : JSON.parse(data.toString());
      if (bytes[0] === 0) ws.send(JSON.stringify({ type: 'heartbeat' }));
      if (bytes[0] === 1) state.reports.push(bytes);
    });
  });
});
const vite = await createViteServer({
  define: {
    'import.meta.env.VITE_SERVER_IP': JSON.stringify('127.0.0.1'),
    'import.meta.env.VITE_SERVER_PORT': JSON.stringify('4317')
  },
  server: { middlewareMode: true, ws: { server } }
});
server.listen(4317, '127.0.0.1', () =>
  console.info('Browser fixture: http://127.0.0.1:4317/tests/browser/index.html')
);
async function close() {
  for (const ws of sockets) ws.terminate();
  wss.close();
  await vite.close();
  server.close();
}
process.once('SIGTERM', () => void close());
process.once('SIGINT', () => void close());
