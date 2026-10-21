import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import test from 'node:test';
import vm from 'node:vm';

import { RtpClock } from '../src/lib/recording-source.ts';

const workerCode = stripTypeScriptTypes(
  readFileSync(
    new URL('../src/pages/desktop/screen/webrtc-record.worker.ts', import.meta.url),
    'utf8'
  )
).replace(/^export .*$/gm, '');
const ownerCode = stripTypeScriptTypes(
  readFileSync(new URL('../src/pages/desktop/screen/webrtc-recording.ts', import.meta.url), 'utf8')
);
const tick = () => new Promise((resolve) => setImmediate(resolve));
function owner(options = {}) {
  const workers = [];
  let failures = 0;
  class Worker {
    messages = [];
    terminated = false;
    constructor() {
      if (options.workerThrows) throw Error('worker denied');
      workers.push(this);
    }
    postMessage(message) {
      this.messages.push(message);
    }
    terminate() {
      this.terminated = true;
    }
  }
  class Transform {
    constructor() {
      if (options.transformThrows) throw Error('transform denied');
    }
  }
  const code = ownerCode
    .slice(ownerCode.indexOf('// A transform worker'))
    .replace(/^export /gm, '');
  const api = vm.runInNewContext(
    '(function(){' + code + ';return {receiverRecording,canRecordReceiver};})()',
    { RecordWorker: Worker, RTCRtpScriptTransform: Transform, RtpClock, Uint8Array }
  );
  const receiver = {};
  const result = api.receiverRecording(
    receiver,
    'h264',
    () => ({ width: 160, height: 90 }),
    () => failures++
  );
  return { api, result, workers, failures: () => failures };
}
test('transform pipeline errors notify the owner instead of silently freezing video', async () => {
  const messages = [],
    self = { postMessage: (m) => messages.push(m) };
  vm.runInNewContext(workerCode, { self, TransformStream, performance });
  self.onmessage({ data: { recording: 1 } });
  const frame = {
    type: 'key',
    getMetadata() {
      throw Error('bad frame metadata');
    }
  };
  self.onrtctransform({
    transformer: {
      sendKeyFrameRequest: async () => {},
      readable: new ReadableStream({
        start(c) {
          c.enqueue(frame);
          c.close();
        }
      }),
      writable: new WritableStream()
    }
  });
  await tick();
  await tick();
  assert.ok(messages.some((m) => m.type === 'transform-error'));
});
test('the always-on transform forwards frames unchanged and only copies while recording', async () => {
  const messages = [],
    forwarded = [],
    self = { postMessage: (m) => messages.push(m) };
  let input;
  vm.runInNewContext(workerCode, { self, TransformStream, performance });
  self.onrtctransform({
    transformer: {
      sendKeyFrameRequest: async () => {},
      readable: new ReadableStream({
        start(c) {
          input = c;
        }
      }),
      writable: new WritableStream({ write: (f) => forwarded.push(f) })
    }
  });
  const frame = {
    type: 'key',
    timestamp: 9000,
    data: new Uint8Array([1, 2, 3]).buffer,
    getMetadata: () => ({ rtpTimestamp: 9000, width: 160, height: 90 })
  };
  input.enqueue(frame);
  await tick();
  assert.equal(forwarded[0], frame);
  assert.equal(messages.length, 0);
  self.onmessage({ data: { recording: 7 } });
  input.enqueue(frame);
  await tick();
  assert.equal(forwarded[1], frame);
  assert.equal(messages[0].recording, 7);
  assert.notEqual(messages[0].data, frame.data);
  assert.deepEqual(new Uint8Array(messages[0].data), new Uint8Array(frame.data));
  input.close();
});
test('explicit pipeline failure triggers fallback once and ends an active recording', () => {
  const f = owner(),
    ends = [];
  f.result.source.start(
    () => {},
    (reason) => ends.push(reason)
  );
  const w = f.workers[0];
  w.onmessage({ data: { type: 'transform-error' } });
  assert.equal(f.failures(), 1);
  w.onerror();
  w.onmessageerror();
  assert.equal(f.failures(), 1);
  assert.equal(f.api.canRecordReceiver(), false);
  assert.deepEqual(ends, ['stream-error']);
  f.result.source.start(
    () => assert.fail(),
    (reason) => ends.push(reason)
  );
  assert.equal(ends.at(-1), 'closed');
  f.result.close();
  assert.ok(w.terminated);
});
test('closed owners ignore late errors from the old connection', () => {
  const f = owner();
  f.result.close();
  f.workers[0].onerror();
  assert.equal(f.failures(), 0);
  assert.equal(f.api.canRecordReceiver(), true);
});
test('worker and transform setup failures choose fallback and release any worker', () => {
  for (const option of ['workerThrows', 'transformThrows']) {
    const f = owner({ [option]: true });
    assert.equal(f.result, null);
    assert.equal(f.failures(), 1);
    assert.equal(f.api.canRecordReceiver(), false);
    assert.ok(f.workers.every((w) => w.terminated));
  }
});
test('late packets from a previous recording never enter the next one', () => {
  const f = owner(),
    seen = [];
  const stop = f.result.source.start(
    () => assert.fail('old recording'),
    () => {}
  );
  stop();
  f.result.source.start(
    (p) => seen.push(p),
    () => {}
  );
  const w = f.workers[0],
    packet = {
      key: true,
      rtpTimestamp: 123,
      width: 160,
      height: 90,
      data: new Uint8Array([1]).buffer
    };
  w.onmessage({ data: { ...packet, recording: 1 } });
  w.onmessage({ data: { ...packet, recording: 2 } });
  assert.equal(seen.length, 1);
  assert.equal(seen[0].timestamp, 0);
  f.result.close();
});
