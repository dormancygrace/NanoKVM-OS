import assert from 'node:assert/strict';
import test from 'node:test';

import { parseMjpegTiles } from '../src/pages/desktop/screen/mjpeg-tiles.ts';

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
