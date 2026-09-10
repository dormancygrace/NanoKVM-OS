export type EncoderTransport = 'direct' | 'webrtc';
export type EncoderCodec = 'h264' | 'h265';

export type EncoderCapabilities = {
  directH265: boolean;
  webRTCH265: boolean;
};

const CODEC_KEY = 'nano-kvm-video-codec';
const DEFAULT_CODEC: EncoderCodec = 'h265';

// CV1812H emits Annex-B H.264 Main Profile, constraint_set1, Level 4.2 and
// H.265 Main Profile, Level 5.0.  Keep the WebCodecs declarations aligned
// with the SPS bytes produced by the hardware encoder.
export const DIRECT_H264_CODEC = 'avc1.4D402A';
export const DIRECT_H265_CODEC = 'hev1.1.6.L150.B0';

export function getEncoderCodec(): EncoderCodec {
  const stored = localStorage.getItem(CODEC_KEY);
  return stored === 'h264' || stored === 'h265' ? stored : DEFAULT_CODEC;
}

export function setEncoderCodec(codec: EncoderCodec) {
  localStorage.setItem(CODEC_KEY, codec);
}

export function encoderCodecQuery(codec: EncoderCodec) {
  return new URLSearchParams({ codec });
}

export async function detectEncoderCapabilities(): Promise<EncoderCapabilities> {
  const directH265 = await supportsDirectH265();
  const webRTCH265 = supportsWebRTCH265();

  return { directH265, webRTCH265 };
}

export async function isEncoderCodecSupported(
  transport: EncoderTransport,
  codec: EncoderCodec
): Promise<boolean> {
  if (codec === 'h264') return true;

  return transport === 'direct' ? supportsDirectH265() : supportsWebRTCH265();
}

async function supportsDirectH265() {
  if (!window.VideoDecoder || !window.isSecureContext) return false;

  try {
    const support = await window.VideoDecoder.isConfigSupported({
      codec: DIRECT_H265_CODEC,
      hardwareAcceleration: 'prefer-hardware',
      optimizeForLatency: true
    });
    return support.supported === true;
  } catch {
    return false;
  }
}

export function supportsWebRTCH265() {
  if (!window.RTCRtpReceiver?.getCapabilities) return false;

  return (
    window.RTCRtpReceiver.getCapabilities('video')?.codecs.some(
      ({ mimeType }) => mimeType.toLowerCase() === 'video/h265'
    ) ?? false
  );
}
