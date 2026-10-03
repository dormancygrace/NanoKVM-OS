import { useAtomValue } from 'jotai';

import { effectiveMouseModeAtom } from '@/jotai/mouse.ts';

import { Absolute } from './absolute.tsx';
import { Relative } from './relative.tsx';

export const Mouse = () => {
  const mouseMode = useAtomValue(effectiveMouseModeAtom);

  if (!mouseMode) return null;
  return <>{mouseMode === 'relative' ? <Relative /> : <Absolute />}</>;
};
