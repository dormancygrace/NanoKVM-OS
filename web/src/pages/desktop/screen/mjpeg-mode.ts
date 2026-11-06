import { getVideoCapabilities } from '@/api/vm.ts';

// Whether the server sends MJPEG tiles. The desktop page asks while it
// preloads the player (players.ts), so the stream element mounts in the same
// commit as the input handlers that attach to #screen.
let tilesSupported: boolean | null = null;

// null until the desktop page asked.
export const mjpegTilesSupported = () => tilesSupported;

export function prepareMjpeg(): Promise<void> {
  return getVideoCapabilities()
    .then((rsp) => {
      tilesSupported = rsp.code === 0 && rsp.data?.stream?.mjpeg?.tiles === true;
    })
    .catch(() => {
      tilesSupported = false;
    });
}
