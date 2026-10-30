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
