import assert from 'node:assert/strict';
import test from 'node:test';

import { parseMjpegTiles, startTileStream } from '../src/pages/desktop/screen/mjpeg-tiles.ts';

// A message as the server builds it (common/mjpeg_tiles.go).
function message(flags, width, height, tiles) {
  const body = tiles.reduce((sum, tile) => sum + tile.jpeg.length, 0);
  const buffer = new ArrayBuffer(8 + 12 * tiles.length + body);
  const view = new DataView(buffer);
  view.setUint8(0, 1);
  view.setUint8(1, flags);
  view.setUint16(2, tiles.length, true);
  view.setUint16(4, width, true);
  view.setUint16(6, height, true);
  let offset = 8 + 12 * tiles.length;
  tiles.forEach((tile, index) => {
    const record = 8 + 12 * index;
    view.setUint16(record, tile.x, true);
    view.setUint16(record + 2, tile.y, true);
    view.setUint16(record + 4, tile.w, true);
    view.setUint16(record + 6, tile.h, true);
    view.setUint32(record + 8, tile.jpeg.length, true);
    new Uint8Array(buffer, offset, tile.jpeg.length).set(tile.jpeg);
    offset += tile.jpeg.length;
  });
  return buffer;
}

test('tiles of a message keep their places and bytes', () => {
  const parsed = parseMjpegTiles(
    message(0, 1920, 1080, [
      { x: 0, y: 952, w: 128, h: 128, jpeg: Uint8Array.of(1, 2, 3) },
      { x: 1792, y: 0, w: 128, h: 128, jpeg: Uint8Array.of(4) }
    ])
  );
  assert.equal(parsed.whole, false);
  assert.equal(parsed.width, 1920);
  assert.equal(parsed.height, 1080);
  assert.deepEqual(
    parsed.tiles.map(({ x, y, width, height, jpeg }) => [x, y, width, height, [...jpeg]]),
    [
      [0, 952, 128, 128, [1, 2, 3]],
      [1792, 0, 128, 128, [4]]
    ]
  );
});

test('a whole picture is flagged', () => {
  const parsed = parseMjpegTiles(
    message(1, 1280, 720, [{ x: 0, y: 0, w: 1280, h: 720, jpeg: Uint8Array.of(9, 9) }])
  );
  assert.equal(parsed.whole, true);
  assert.equal(parsed.tiles.length, 1);
});

test('malformed messages are refused', () => {
  const good = message(0, 256, 256, [{ x: 128, y: 128, w: 128, h: 128, jpeg: Uint8Array.of(1) }]);
  assert.ok(parseMjpegTiles(good));
  // Truncated, trailing bytes, other version, tile outside the picture, no tiles.
  assert.equal(parseMjpegTiles(good.slice(0, good.byteLength - 1)), null);
  const longer = new Uint8Array(good.byteLength + 1);
  longer.set(new Uint8Array(good));
  assert.equal(parseMjpegTiles(longer.buffer), null);
  const version = good.slice(0);
  new DataView(version).setUint8(0, 2);
  assert.equal(parseMjpegTiles(version), null);
  assert.equal(
    parseMjpegTiles(
      message(0, 256, 256, [{ x: 200, y: 0, w: 128, h: 128, jpeg: Uint8Array.of(1) }])
    ),
    null
  );
  assert.equal(parseMjpegTiles(message(0, 256, 256, [])), null);
});

// A fake connection, decoder and clock for startTileStream.
function harness(maxPending = 2) {
  const sockets = [];
  const decodes = [];
  const draws = [];
  const timers = [];
  const stop = startTileStream({
    connect: () => {
      const socket = { binaryType: '', onmessage: null, onclose: null, closed: false };
      socket.close = () => {
        socket.closed = true;
      };
      sockets.push(socket);
      return socket;
    },
    decode: (tile) =>
      new Promise((resolve) => decodes.push(() => resolve({ value: tile.jpeg[0], close() {} }))),
    draw: (message, decoded, first) =>
      draws.push({
        whole: message.whole,
        first,
        values: decoded.map(({ bitmap }) => bitmap.value)
      }),
    later: (callback) => {
      timers.push(callback);
      return () => {};
    },
    maxPending
  });
  const send = (socket, whole, value) =>
    socket.onmessage({
      data: message(whole ? 1 : 0, 256, 256, [
        { x: 0, y: 0, w: 128, h: 128, jpeg: Uint8Array.of(value) }
      ])
    });
  const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
  return { sockets, decodes, draws, timers, stop, send, settle };
}

test('a connection starts with a whole picture and draws in order', async () => {
  const h = harness();
  const [socket] = h.sockets;
  h.send(socket, false, 1); // before the first whole picture: ignored
  h.send(socket, true, 2);
  h.send(socket, false, 3);
  assert.equal(h.decodes.length, 2);
  h.decodes[1]();
  await h.settle();
  assert.equal(h.draws.length, 0, 'the later message waits for the earlier one');
  h.decodes[0]();
  await h.settle();
  assert.deepEqual(h.draws, [
    { whole: true, first: true, values: [2] },
    { whole: false, first: false, values: [3] }
  ]);
  h.stop();
  assert.ok(socket.closed);
});

test('a backlog closes the connection and the next one draws while old decodes hang', async () => {
  const h = harness(2);
  const [old] = h.sockets;
  h.send(old, true, 1);
  h.send(old, false, 2);
  h.send(old, false, 3); // a third waiting message: the connection starts again
  assert.ok(old.closed);
  assert.equal(h.decodes.length, 2, 'the refused message is not decoded or counted');
  old.onclose();
  assert.equal(h.timers.length, 1);
  h.timers[0]();
  const next = h.sockets[1];
  h.send(next, true, 4);
  h.decodes[2]();
  await h.settle();
  assert.deepEqual(h.draws, [{ whole: true, first: true, values: [4] }]);
  // The old decodes finish late: nothing of the old connection is drawn,
  // and the new one keeps taking messages up to its own limit.
  h.decodes[0]();
  h.decodes[1]();
  await h.settle();
  assert.equal(h.draws.length, 1);
  h.send(next, false, 5);
  h.send(next, false, 6);
  assert.ok(!next.closed);
  h.stop();
});

test('every stream reports its first picture, also at the same size', async () => {
  for (let run = 0; run < 2; run++) {
    const h = harness();
    h.send(h.sockets[0], true, 7);
    h.decodes[0]();
    await h.settle();
    assert.equal(h.draws[0].first, true);
    h.stop();
  }
});
