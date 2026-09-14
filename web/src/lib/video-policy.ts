import { getScreen } from '@/api/vm';

import type { EncoderCodec } from './encoder';

export function isQhdStream(height: number, inputWidth = 0, inputHeight = 0) {
  // A zero stream height means “same as input”. The qualified standard
  // portrait inputs and the legacy aligned input are below the QHD limit.
  if (
    height === 0 &&
    ((inputWidth === 720 && inputHeight === 1280) ||
      ((inputWidth === 1080 || inputWidth === 1088) && inputHeight === 1920))
  ) {
    return false;
  }
  return height > 1080 || (height === 0 && (inputWidth > 1920 || inputHeight > 1080));
}

// Re-read shared device settings before changing a browser-only mode or codec.
export async function isQhdWebRTCBlocked(mode: string, codec: EncoderCodec, height?: number) {
  if (mode !== 'h264' || codec !== 'h265') return false;
  const rsp = await getScreen();
  if (rsp.code !== 0) throw new Error(rsp.msg);
  return isQhdStream(height ?? rsp.data.height, rsp.data.inputWidth, rsp.data.inputHeight);
}

// All menu entry points use the same maximum-profile restriction as Settings.
export async function isMaximumPortraitBlocked(mode: string, codec: EncoderCodec) {
  if (mode === 'direct' && codec === 'h265') return false;
  const rsp = await getScreen();
  if (rsp.code !== 0) throw new Error(rsp.msg);
  return (
    (rsp.data.portrait && rsp.data.portraitResolution === 2560) ||
    (rsp.data.inputWidth === 1440 && rsp.data.inputHeight === 2560)
  );
}
