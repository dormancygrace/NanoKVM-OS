// Tiled MJPEG messages (server common/mjpeg_tiles.go, kvm_vision.h): the
// JPEG tiles of the parts of the picture that changed, little-endian:
//   u8 version (1), u8 flags (1: the whole picture), u16 count,
//   u16 picture width, u16 picture height,
//   count x {u16 x, u16 y, u16 w, u16 h, u32 bytes}, then the JPEGs in order.

export type MjpegTile = {
  x: number;
  y: number;
  width: number;
  height: number;
  jpeg: Uint8Array<ArrayBuffer>;
};

export type MjpegTilesMessage = {
  whole: boolean;
  width: number;
  height: number;
  tiles: MjpegTile[];
};

const HEADER = 8;
const RECORD = 12;

export function parseMjpegTiles(buffer: ArrayBuffer): MjpegTilesMessage | null {
  if (buffer.byteLength < HEADER) return null;
  const view = new DataView(buffer);
  if (view.getUint8(0) !== 1) return null;
  const count = view.getUint16(2, true);
  const width = view.getUint16(4, true);
  const height = view.getUint16(6, true);
  let offset = HEADER + RECORD * count;
  if (!count || !width || !height || offset > buffer.byteLength) return null;
  const tiles: MjpegTile[] = [];
  for (let index = 0; index < count; index++) {
    const record = HEADER + RECORD * index;
    const tile = {
      x: view.getUint16(record, true),
      y: view.getUint16(record + 2, true),
      width: view.getUint16(record + 4, true),
      height: view.getUint16(record + 6, true)
    };
    const bytes = view.getUint32(record + 8, true);
    if (
      !bytes ||
      offset + bytes > buffer.byteLength ||
      tile.x + tile.width > width ||
      tile.y + tile.height > height
    ) {
      return null;
    }
    tiles.push({ ...tile, jpeg: new Uint8Array(buffer, offset, bytes) });
    offset += bytes;
  }
  if (offset !== buffer.byteLength) return null;
  return { whole: (view.getUint8(1) & 1) !== 0, width, height, tiles };
}

export type TileSocket = Pick<WebSocket, 'binaryType' | 'onmessage' | 'onclose' | 'close'>;
export type DecodedTile<B> = { tile: MjpegTile; bitmap: B };

type TileStreamOptions<B extends { close: () => void }> = {
  connect: () => TileSocket;
  decode: (tile: MjpegTile) => Promise<B>;
  // first: the first picture drawn since the stream started.
  draw: (message: MjpegTilesMessage, decoded: DecodedTile<B>[], first: boolean) => void;
  later: (callback: () => void, ms: number) => () => void;
  // Messages of one connection waiting to be decoded before it starts again
  // from a whole picture (the server sends one to every new connection).
  maxPending?: number;
};

const INITIAL_RETRY_DELAY = 1000;
const MAX_RETRY_DELAY = 10000;

// The tiled MJPEG connection: messages build on each other, so a connection
// decodes in parallel and draws in order, and starts with a whole picture.
// The queue belongs to its connection: a new one never waits for the
// decodes of an old one. It reconnects with backoff until stopped.
export function startTileStream<B extends { close: () => void }>(options: TileStreamOptions<B>) {
  const maxPending = options.maxPending ?? 30;
  let socket: TileSocket | null = null;
  let stopped = false;
  let first = true;
  let retryDelay = INITIAL_RETRY_DELAY;
  let cancelRetry: (() => void) | undefined;

  const connect = () => {
    const current = (socket = options.connect());
    const live = () => !stopped && socket === current;
    let drawn = Promise.resolve();
    let pending = 0;
    let waitWhole = true;
    current.binaryType = 'arraybuffer';

    current.onmessage = (event) => {
      if (!live()) return;
      const message = parseMjpegTiles(event.data as ArrayBuffer);
      if (!message || (waitWhole && !message.whole)) return;
      if (pending >= maxPending) {
        // Decoding cannot keep up: start again from a whole picture.
        current.close();
        return;
      }
      waitWhole = false;
      retryDelay = INITIAL_RETRY_DELAY;
      pending++;
      const bitmaps = Promise.all(
        message.tiles.map((tile) => options.decode(tile).then((bitmap) => ({ tile, bitmap })))
      );
      drawn = drawn
        .then(() => bitmaps)
        .then(
          (decoded) => {
            pending--;
            if (live()) {
              options.draw(message, decoded, first);
              first = false;
            }
            decoded.forEach(({ bitmap }) => bitmap.close());
          },
          () => {
            pending--;
            // A tile that did not decode leaves the picture behind.
            if (live()) current.close();
          }
        );
    };

    current.onclose = () => {
      if (!live()) return;
      socket = null;
      cancelRetry = options.later(connect, retryDelay);
      retryDelay = Math.min(retryDelay * 2, MAX_RETRY_DELAY);
    };
  };
  connect();

  return () => {
    stopped = true;
    cancelRetry?.();
    socket?.close();
    socket = null;
  };
}
