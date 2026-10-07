import { useEffect, useState } from 'react';
import { useAtomValue } from 'jotai';

import { videoModeAtom } from '@/jotai/screen.ts';

import { getLoadedPlayer, hasPlayer, loadPlayer, type PlayerProps } from './players.ts';

// The desktop page preloads the player before it first renders the screen, so
// the player normally mounts in the same commit as the input handlers that
// attach to its #screen element. Otherwise nothing is shown until it loads.
const PlayerHost = ({ mode, ...props }: PlayerProps & { mode: string }) => {
  const [Player, setPlayer] = useState(() => getLoadedPlayer(mode));
  const [error, setError] = useState<unknown>(null);

  useEffect(() => {
    if (Player) return;
    let active = true;
    loadPlayer(mode)?.then(
      (player) => active && setPlayer(() => player),
      (reason: unknown) => active && setError(reason)
    );
    return () => {
      active = false;
    };
  }, [mode, Player]);

  // Surface a failed chunk request to the error boundary, as a static import would.
  if (error) throw error;
  return Player ? <Player {...props} /> : null;
};

export const Screen = ({ onEncoderConflict }: PlayerProps) => {
  const videoMode = useAtomValue(videoModeAtom);

  if (!hasPlayer(videoMode)) {
    return null;
  }

  return <PlayerHost key={videoMode} mode={videoMode} onEncoderConflict={onEncoderConflict} />;
};
