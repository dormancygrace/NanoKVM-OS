import { createServer } from 'node:http';
import { createServer as createViteServer } from 'vite';
import { WebSocketServer } from 'ws';

// No device proxy and no credentials from the environment. Only loopback.
const state = { loggedIn: false, connections: 0, attempts: 0, reports: [], loginEncoding: '' };
const sockets = new Set();
let pauseUpgrades = false;
const pendingUpgrades = new Map();
const server = createServer(async (req, res) => {
  const pathname = new URL(req.url, 'http://localhost').pathname;
  const json = (data, status = 200) => {
    res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
    res.end(JSON.stringify(data));
  };
  const ok = (data) => json({ code: 0, msg: 'success', data });
  if (pathname === '/__test/state')
    return json({ ...state, pendingUpgrades: pendingUpgrades.size });
  if (pathname.startsWith('/__test/')) {
    if (req.method !== 'POST') return json({ error: 'POST required' }, 405);
    if (pathname === '/__test/reset') {
      pauseUpgrades = false;
      for (const socket of pendingUpgrades.keys()) socket.destroy();
      pendingUpgrades.clear();
      Object.assign(state, {
        loggedIn: false,
        connections: 0,
        attempts: 0,
        reports: [],
        loginEncoding: ''
      });
      for (const socket of sockets) socket.close();
    } else if (pathname === '/__test/upgrades/pause') pauseUpgrades = true;
    else if (pathname === '/__test/upgrades/resume') {
      pauseUpgrades = false;
      const upgrades = [...pendingUpgrades.values()];
      pendingUpgrades.clear();
      for (const upgrade of upgrades) upgrade();
    } else if (pathname === '/__test/reports/reset') state.reports = [];
    else if (pathname === '/__test/drop') {
      for (const socket of sockets) socket.close(1012, 'fixture restart');
    } else if (pathname === '/__test/expire') {
      state.loggedIn = false;
      for (const socket of sockets) socket.close(4401, 'fixture expiry');
    } else return json({ error: 'Unknown test endpoint' }, 404);
    return ok(null);
  }
  if (pathname === '/api/vm/web-title') return ok({ title: '' });
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
  if (pathname === '/api/vm/device/virtual' && req.method === 'GET')
    return ok({
      keyboard: false,
      relative: false,
      absolute: false,
      network: false,
      disk: false,
      serial: false,
      audio: false,
      hid: false,
      mode: 'normal',
      revision: 'fixture-only',
      budget: { inUsed: 0, outUsed: 0, inLimit: 6, outLimit: 7 },
      costs: {
        keyboard: { in: 1, out: 1 },
        relative: { in: 1, out: 1 },
        absolute: { in: 1, out: 1 },
        network: { in: 2, out: 1 },
        disk: { in: 1, out: 1 },
        serial: { in: 2, out: 1 },
        audio: { in: 0, out: 1 }
      }
    });
  if (pathname === '/api/vm/usb-internet' && req.method === 'GET')
    return ok({
      enabled: false,
      state: 'disabled',
      flowOffload: false,
      offloadReason: '',
      ipv6: false
    });
  if (pathname.startsWith('/api/')) return json({ error: 'No fixture for ' + pathname }, 404);
  vite.middlewares(req, res);
});
const wss = new WebSocketServer({ noServer: true });
server.on('upgrade', (request, socket, head) => {
  if (new URL(request.url, 'http://localhost').pathname !== '/api/ws') return;
  const upgrade = () => {
    if (socket.destroyed) return;
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
  };
  // Hold the handshake, not a transient UI state or a fixed reconnect delay.
  if (pauseUpgrades) {
    pendingUpgrades.set(socket, upgrade);
    socket.once('close', () => pendingUpgrades.delete(socket));
  } else upgrade();
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
  for (const socket of pendingUpgrades.keys()) socket.destroy();
  pendingUpgrades.clear();
  for (const ws of sockets) ws.terminate();
  wss.close();
  await vite.close();
  server.close();
}
process.once('SIGTERM', () => void close());
process.once('SIGINT', () => void close());
