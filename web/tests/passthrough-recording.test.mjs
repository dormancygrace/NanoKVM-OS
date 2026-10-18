import assert from 'node:assert/strict';
import test from 'node:test';

import { recordPassthrough } from '../src/lib/passthrough-recording.ts';

// One real 160x90 Main-profile IDR with SPS/PPS (ffmpeg color source).
const data = Uint8Array.from(
  Buffer.from(
    'AAAAAWdNQAvsoUb8mAiAAAADAIAAABkHihTLAAAAAWjr48sgAAABZYiEACv//tjn8yyn9Nq78KzEmYqlw6svdwqUDRlDPLdLXbPTOo5vSpIZwA+gAIyDnGU=',
    'base64'
  )
);
function fixture(options = {}) {
  const events = [],
    chunks = [];
  let packet, end;
  const source = {
    codec: 'h264',
    start(onPacket, onEnd) {
      packet = onPacket;
      end = onEnd;
      if (options.closed) onEnd('closed');
      return () => events.push('stop-source');
    }
  };
  const writable = new WritableStream({
    write(chunk) {
      if (options.diskError) throw Error('disk full');
      chunks.push(chunk);
    },
    close() {
      events.push('commit');
    },
    abort() {
      events.push('abort');
    }
  });
  const send = (timestamp, bytes = data) =>
    packet({ key: true, timestamp, width: 160, height: 90, data: bytes });
  return { source, writable, events, chunks, send, packet: (p) => packet(p), end: () => end('closed') };
}
const tick = () => new Promise((resolve) => setImmediate(resolve));
test('MP4 commits only after successful finalization', async () => {
  const f = fixture();
  const s = await recordPassthrough(f.source, f.writable, null);
  f.send(0);
  await s.started;
  f.send(40000);
  await tick();
  assert.ok(!f.events.includes('commit'));
  s.stop();
  await s.done;
  assert.ok(f.chunks.some((c) => Buffer.from(c.data).includes(Buffer.from('ftyp'))));
  assert.equal(f.events.filter((e) => e === 'commit').length, 1);
  assert.ok(!f.events.includes('abort'));
});
test('a malformed video packet aborts instead of committing a partial file', async () => {
  const f = fixture();
  const s = await recordPassthrough(f.source, f.writable, null);
  const rejected = assert.rejects(s.done);
  f.send(0, new Uint8Array([1, 2, 3]));
  await s.started;
  f.send(40000);
  s.stop();
  await rejected;
  assert.ok(f.events.includes('abort'));
  assert.ok(!f.events.includes('commit'));
});
test('storage failure aborts and releases the source', async () => {
  const f = fixture({ diskError: true });
  const s = await recordPassthrough(f.source, f.writable, null);
  const rejected = assert.rejects(s.done, /disk full/);
  f.send(0);
  s.stop();
  await rejected;
  assert.ok(f.events.includes('stop-source'));
  assert.ok(!f.events.includes('commit'));
});
test('a source already disposed during async preparation settles the session', async () => {
  const f = fixture({ closed: true });
  const s = await recordPassthrough(f.source, f.writable, null);
  await assert.rejects(s.done, /recording-no-video/);
  assert.ok(f.events.includes('abort'));
  assert.ok(f.events.includes('stop-source'));
});
test('waiting for a keyframe can be canceled without creating a file', async () => {
  const f = fixture();
  const s = await recordPassthrough(f.source, f.writable, null);
  s.stop();
  s.stop();
  await assert.rejects(s.done, /recording-no-video/);
  assert.ok(f.events.includes('abort'));
  assert.ok(!f.events.includes('commit'));
});
test('packets retained inside a long MP4 fragment count towards the memory limit', async () => {
  const f = fixture();
  const s = await recordPassthrough(f.source, f.writable, null, 1000);
  const rejected = assert.rejects(s.done, /recording-backpressure/);
  f.send(0);
  await s.started;
  for (let i = 1; i < 40; i++) {
    f.send(i * 10000);
    await tick();
  }
  s.stop();
  await rejected;
  assert.ok(f.events.includes('abort'));
  assert.ok(!f.events.includes('commit'));
});
test('a recording asks for a keyframe when the last one is 5 s old', async () => {
  const f = fixture();
  let requests = 0;
  f.source.requestKeyframe = () => requests++;
  const s = await recordPassthrough(f.source, f.writable, null);
  f.send(0);
  await s.started;
  const delta = (timestamp) => f.packet({ key: false, timestamp, width: 160, height: 90, data });
  for (let second = 1; second <= 9; second++) delta(second * 1_000_000);
  // Packets come out one frame late: 5 s is out after the frame at 6 s.
  assert.equal(requests, 1);
  f.send(10_000_000);
  for (let second = 11; second <= 16; second++) delta(second * 1_000_000);
  assert.equal(requests, 2);
  s.stop();
  await s.done;
});
