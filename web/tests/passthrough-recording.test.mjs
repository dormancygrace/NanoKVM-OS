import assert from 'node:assert/strict';
import test from 'node:test';
import { ALL_FORMATS, BufferSource, EncodedPacketSink, Input } from 'mediabunny';

import { recordPassthrough, SILENCE_LAG } from '../src/lib/passthrough-recording.ts';
import { AUDIO_RESYNC_SECONDS, OPUS_SILENCE } from '../src/lib/recording-audio.ts';

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
  return {
    source,
    writable,
    events,
    chunks,
    send,
    packet: (p) => packet(p),
    end: (reason = 'closed') => end(reason)
  };
}
const tick = () => new Promise((resolve) => setImmediate(resolve));
function fileInput(f) {
  let size = 0;
  for (const c of f.chunks) size = Math.max(size, c.position + c.data.byteLength);
  const bytes = new Uint8Array(size);
  for (const c of f.chunks)
    bytes.set(
      new Uint8Array(c.data.buffer ?? c.data, c.data.byteOffset ?? 0, c.data.byteLength),
      c.position
    );
  return new Input({ source: new BufferSource(bytes), formats: ALL_FORMATS });
}
test('source failure saves a playable prefix and reports interruption', async () => {
  const f = fixture();
  const s = await recordPassthrough(f.source, f.writable, null);
  f.send(0);
  await s.started;
  f.send(40000);
  f.end('stream-error');
  assert.equal((await s.done).reason, 'stream-error');
  assert.ok(f.events.includes('commit'));
  assert.ok(!f.events.includes('abort'));
  const input = fileInput(f);
  const track = await input.getPrimaryVideoTrack();
  assert.ok(await new EncodedPacketSink(track).getFirstPacket());
  input.dispose();
});
test('received Opus packets reach MP4 unchanged, with arrival offset and RTP gaps', async () => {
  const f = fixture();
  let packet, end;
  let stopped = false;
  const raw = {
    start(p, e) {
      packet = p;
      end = e;
      return () => {
        stopped = true;
      };
    }
  };
  const s = await recordPassthrough(f.source, f.writable, null, undefined, raw);
  f.packet({ key: true, timestamp: 0, receivedAt: 1000, width: 160, height: 90, data });
  await s.started;
  const opus = new Uint8Array([0xfc, 0xff, 0xfe]);
  packet({ data: opus, timestamp: 0, receivedAt: 1040 });
  packet({ data: opus, timestamp: 20000, receivedAt: 1060 });
  // Temporary packet loss is a gap, not an irreversible close or time compression.
  packet({ data: opus, timestamp: 220000, receivedAt: 1260 });
  f.send(300000);
  await tick();
  end('stopped');
  s.stop();
  assert.equal((await s.done).reason, undefined);
  assert.ok(stopped);
  const input = fileInput(f);
  const track = await input.getPrimaryAudioTrack();
  assert.equal(track.codec, 'opus');
  assert.equal(await track.getNumberOfChannels(), 2);
  const packets = [];
  for await (const p of new EncodedPacketSink(track).packets()) packets.push(p);
  assert.equal(packets.length, 3);
  for (const p of packets) assert.deepEqual(p.data, opus);
  for (const [i, seconds] of [0.04, 0.06, 0.26].entries())
    assert.ok(Math.abs(packets[i].timestamp - seconds) < 0.0001, String(packets[i].timestamp));
  input.dispose();
});

// The sender's RTP clock stands still while the computer plays nothing; the
// MP4 interleaver would wait for audio and hold the video until the limit.
test('a pause in received audio is filled with silence and does not end the file', async () => {
  assert.ok(SILENCE_LAG > AUDIO_RESYNC_SECONDS);
  const f = fixture();
  let packet;
  const raw = {
    start(p) {
      packet = p;
      return () => {};
    }
  };
  const s = await recordPassthrough(f.source, f.writable, null, 20000, raw);
  f.packet({ key: true, timestamp: 0, receivedAt: 1000, width: 160, height: 90, data });
  await s.started;
  const opus = new Uint8Array([0xfc, 0xff, 0xfe, 0x01]);
  packet({ data: opus, timestamp: 0, receivedAt: 1000 });
  // 12 s of video with no audio: far more video than the limit holds
  for (let i = 1; i <= 300; i++) {
    f.send(i * 40000);
    if (i % 10 === 0) await tick();
  }
  // audio resumes; its RTP clock went on from the last packet
  packet({ data: opus, timestamp: 20000, receivedAt: 1000 + 12000 });
  packet({ data: opus, timestamp: 40000, receivedAt: 1000 + 12020 });
  f.send(301 * 40000);
  await tick();
  s.stop();
  assert.equal((await s.done).reason, undefined);
  const input = fileInput(f);
  const track = await input.getPrimaryAudioTrack();
  const packets = [];
  for await (const p of new EncodedPacketSink(track).packets()) packets.push(p);
  const real = packets.filter((p) => p.data.byteLength === opus.byteLength);
  assert.equal(real.length, 3);
  assert.ok(packets.length > 3, 'no silence was added');
  for (const p of packets.filter((p) => p.data.byteLength !== opus.byteLength))
    assert.deepEqual(p.data, OPUS_SILENCE);
  for (let i = 1; i < packets.length; i++)
    assert.ok(packets[i].timestamp > packets[i - 1].timestamp);
  // resumed audio sits at its arrival time, not right after the first packet
  assert.ok(Math.abs(real[1].timestamp - 12) < 0.0001, String(real[1].timestamp));
  assert.ok(Math.abs(real[2].timestamp - 12.02) < 0.0001, String(real[2].timestamp));
  input.dispose();
});
