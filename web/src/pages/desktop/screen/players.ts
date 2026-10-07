import type { ComponentType } from 'react';

export type PlayerProps = { onEncoderConflict: () => boolean };
type Player = ComponentType<PlayerProps>;

// Only one player is used per session, so each one is a separate chunk.
const imports = new Map<string, () => Promise<Player>>([
  ['mjpeg', () => import('./mjpeg.tsx').then((module) => module.Mjpeg)],
  ['direct', () => import('./h264-direct.tsx').then((module) => module.H264Direct)],
  ['h264', () => import('./h264-webrtc.tsx').then((module) => module.H264Webrtc)]
]);

const loaded = new Map<string, Player>();
const pending = new Map<string, Promise<Player>>();

export function hasPlayer(mode: string): boolean {
  return imports.has(mode);
}

export function getLoadedPlayer(mode: string): Player | undefined {
  return loaded.get(mode);
}

export function loadPlayer(mode: string): Promise<Player> | undefined {
  const load = imports.get(mode);
  if (!load) return undefined;
  let promise = pending.get(mode);
  if (!promise) {
    promise = load().then((player) => {
      loaded.set(mode, player);
      return player;
    });
    // A failed chunk request may be retried by the next caller.
    promise.catch(() => pending.delete(mode));
    pending.set(mode, promise);
  }
  return promise;
}

// Resolves (never rejects) once the player for `mode` can render synchronously.
export function preloadPlayer(mode: string): Promise<void> {
  return Promise.resolve(loadPlayer(mode)).then(
    () => undefined,
    () => undefined
  );
}
