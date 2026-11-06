import assert from 'node:assert/strict';
import test from 'node:test';

import {
  PROBE_PACE,
  probeTimeout,
  replyWindow,
  STALL_AFTER,
  StallCheck,
  StallProbe
} from '../src/pages/desktop/screen/stall-check.ts';

// A real clock never reads 0, which the check takes for no frame yet.
const t = (ms) => 100_000 + ms;

const lan = () => {
  const stall = new StallCheck();
  stall.opened(30);
  stall.frame(t(0));
  return stall;
};

test('nothing happens before the first frame or while frames arrive', () => {
  const stall = new StallCheck();
  assert.equal(stall.step(t(60_000)), 'none');
  stall.frame(t(0));
  assert.equal(stall.step(t(STALL_AFTER - 1)), 'none');
});

test('a frozen video sends one heartbeat, then waits for the reply window', () => {
  const stall = lan();
  assert.equal(stall.step(t(1000)), 'heartbeat');
  assert.equal(stall.step(t(1100)), 'none');
  assert.equal(stall.step(t(1399)), 'none');
  assert.equal(stall.step(t(1400)), 'probe');
});

test('an answered heartbeat means alive: no probe, and the check starts over', () => {
  const stall = lan();
  stall.step(t(1000));
  stall.reply(t(1005));
  assert.equal(stall.step(t(1100)), 'none');
  assert.equal(stall.step(t(2099)), 'none');
  assert.equal(stall.step(t(2100)), 'heartbeat');
});

test('a probe that opens confirms a dead connection only while frozen and unanswered', () => {
  const stall = lan();
  stall.step(t(1000));
  assert.equal(stall.step(t(1400)), 'probe');
  assert.ok(stall.confirmsDead(t(1450)));
});

test('a frame that arrives while the probe connects cancels the reconnect', () => {
  const stall = lan();
  stall.step(t(1000));
  stall.step(t(1400));
  stall.frame(t(1420));
  assert.ok(!stall.confirmsDead(t(1450)));
  assert.ok(!stall.heartbeatUnanswered);
});

test('a late reply that arrives while the probe connects cancels the reconnect', () => {
  const stall = lan();
  stall.step(t(1000));
  stall.step(t(1400));
  stall.reply(t(1430));
  assert.ok(!stall.confirmsDead(t(1450)));
});

test('a slow link gets a reply window and probe timeout from its open time', () => {
  // About 600 ms round trip: the signaling socket took 2.1 s to open.
  assert.equal(replyWindow(2100), 2100);
  assert.equal(probeTimeout(2100), 4200);
  assert.equal(replyWindow(30), 400);
  assert.equal(probeTimeout(30), 800);
  const stall = new StallCheck();
  stall.opened(2100);
  stall.frame(t(0));
  stall.step(t(1000));
  assert.equal(stall.step(t(1000 + 600)), 'none');
  stall.reply(t(1000 + 650));
  assert.equal(stall.step(t(1000 + 700)), 'none');
  assert.equal(stall.probeTimeout(), 4200);
});

test('a page shown again restarts the clock', () => {
  const stall = lan();
  stall.shown(t(5000));
  assert.equal(stall.step(t(5500)), 'none');
  assert.equal(stall.step(t(6000)), 'heartbeat');
});

test('a page shown again during a check sends a fresh heartbeat before any probe', () => {
  const stall = lan();
  assert.equal(stall.step(t(1000)), 'heartbeat');
  stall.shown(t(5000));
  assert.ok(!stall.heartbeatUnanswered);
  assert.equal(stall.step(t(5500)), 'none');
  assert.equal(stall.step(t(6000)), 'heartbeat');
});

// A WebSocket stand-in: the test opens or ends it.
const fakeSocket = () => {
  const socket = {
    readyState: WebSocket.CONNECTING,
    onopen: null,
    onclose: null,
    closes: 0,
    close() {
      socket.closes++;
      if (socket.readyState === WebSocket.CLOSED) return;
      socket.readyState = WebSocket.CLOSED;
      socket.onclose?.();
    },
    open() {
      socket.readyState = WebSocket.OPEN;
      socket.onopen?.();
    }
  };
  return socket;
};

const host = (overrides = {}) => {
  const h = {
    clock: t(0),
    checking: true,
    input: true,
    heartbeats: 0,
    probes: [],
    deaths: 0,
    now: () => h.clock,
    canCheck: () => h.checking,
    inputConnected: () => h.input,
    heartbeat: () => h.heartbeats++,
    openProbe: () => {
      const socket = fakeSocket();
      h.probes.push(socket);
      return socket;
    },
    dead: () => h.deaths++,
    ...overrides
  };
  return h;
};

const frozen = () => {
  const h = host();
  const prober = new StallProbe(lan(), h);
  h.clock = t(1000);
  prober.tick();
  h.clock = t(1400);
  return { h, prober };
};

test('the prober sends the heartbeat, then opens one probe', () => {
  const { h, prober } = frozen();
  assert.equal(h.heartbeats, 1);
  prober.tick();
  prober.tick();
  assert.equal(h.probes.length, 1);
  h.probes[0].close();
});

test('an opened probe on a frozen, silent connection reports it dead once', () => {
  const { h, prober } = frozen();
  prober.tick();
  h.probes[0].open();
  assert.equal(h.deaths, 1);
  assert.equal(h.probes[0].readyState, WebSocket.CLOSED);
});

test('a reply while the probe connects keeps the connection', () => {
  const h = host();
  const stall = lan();
  const prober = new StallProbe(stall, h);
  h.clock = t(1000);
  prober.tick();
  h.clock = t(1400);
  prober.tick();
  stall.reply(t(1420));
  h.probes[0].open();
  assert.equal(h.deaths, 0);
});

test('no probe while the input socket is down, and a connecting one is closed', () => {
  const { h, prober } = frozen();
  h.input = false;
  prober.tick();
  assert.equal(h.probes.length, 0);
  h.input = true;
  prober.tick();
  assert.equal(h.probes.length, 1);
  h.input = false;
  prober.tick();
  assert.equal(h.probes[0].readyState, WebSocket.CLOSED);
  h.input = true;
  h.clock = t(1400 + PROBE_PACE);
  prober.tick();
  assert.equal(h.probes.length, 2);
  h.probes[1].close();
});

test('nothing runs while the host cannot check', () => {
  const h = host({ checking: false });
  h.checking = false;
  const prober = new StallProbe(lan(), h);
  h.clock = t(5000);
  prober.tick();
  assert.equal(h.heartbeats, 0);
  assert.equal(h.probes.length, 0);
});

test('a probe that never opens is closed after the probe timeout', async () => {
  const { h, prober } = frozen();
  prober.tick();
  await new Promise((resolve) => setTimeout(resolve, probeTimeout(30) + 50));
  assert.equal(h.probes[0].readyState, WebSocket.CLOSED);
  assert.equal(h.deaths, 0);
});

test('after dispose a probe that opens reports nothing', () => {
  const { h, prober } = frozen();
  prober.tick();
  const probe = h.probes[0];
  prober.dispose();
  assert.equal(probe.readyState, WebSocket.CLOSED);
  probe.readyState = WebSocket.CONNECTING;
  probe.open();
  assert.equal(h.deaths, 0);
  prober.tick();
  assert.equal(h.probes.length, 1);
});

test('a refused probe is replaced no sooner than PROBE_PACE', () => {
  const { h, prober } = frozen();
  prober.tick();
  h.probes[0].close();
  h.clock = t(1400 + PROBE_PACE - 1);
  prober.tick();
  assert.equal(h.probes.length, 1);
  h.clock = t(1400 + PROBE_PACE);
  prober.tick();
  assert.equal(h.probes.length, 2);
  h.probes[1].close();
});
