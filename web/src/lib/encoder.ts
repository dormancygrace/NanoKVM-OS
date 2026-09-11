export type EncoderTransport = 'direct' | 'webrtc';
export type EncoderCodec = 'h264' | 'h265';

export type EncoderCapabilities = {
  directH265: boolean;
  webRTCH265: boolean;
};

const CODEC_KEY = 'nano-kvm-video-codec';
let effectiveCodec: EncoderCodec | undefined;
let directH265Support: Promise<boolean> | undefined;

// CV1812H emits Annex-B H.264 Main Profile, constraint_set1, Level 4.2 and
// H.265 Main Profile, Level 5.0.  Keep the WebCodecs declarations aligned
// with the SPS bytes produced by the hardware encoder.
export const DIRECT_H264_CODEC = 'avc1.4D402A';
export const DIRECT_H265_CODEC = 'hev1.1.6.L150.B0';

function getStoredCodec(): EncoderCodec | undefined {
  try {
    const stored = localStorage.getItem(CODEC_KEY);
    return stored === 'h264' || stored === 'h265' ? stored : undefined;
  } catch {
    return undefined;
  }
}

export function getEncoderCodec(): EncoderCodec {
  return effectiveCodec ?? getStoredCodec() ?? 'h264';
}

// Resolve before mounting the video player and codec menu. Automatic fallback
// belongs to this page session, not to the user's persisted manual preference.
export async function initializeEncoderCodec(
  transport: EncoderTransport,
  activeCodec?: EncoderCodec
): Promise<EncoderCodec> {
  if (activeCodec) {
    // A joining browser follows the existing encoder without overwriting its
    // saved preference or silently reconfiguring other viewers' stream.
    effectiveCodec = activeCodec;
    if (!(await isEncoderCodecSupported(transport, activeCodec))) {
      throw new Error('active-codec-unsupported');
    }
    return activeCodec;
  }
  const requested = getStoredCodec() ?? 'h265';
  const supported = await isEncoderCodecSupported(transport, requested);
  effectiveCodec = supported ? requested : 'h264';
  return effectiveCodec;
}

export function setEncoderCodec(codec: EncoderCodec) {
  effectiveCodec = codec;
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

function supportsDirectH265(): Promise<boolean> {
  return (directH265Support ??= probeDirectH265());
}

async function probeDirectH265() {
  if (!window.VideoDecoder || !window.isSecureContext) return false;

  try {
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      return await Promise.race([
        window.VideoDecoder.isConfigSupported({
          codec: DIRECT_H265_CODEC,
          hardwareAcceleration: 'prefer-hardware',
          optimizeForLatency: true
        }).then((support) => support.supported === true),
        new Promise<false>((resolve) => {
          timer = setTimeout(() => resolve(false), 2000);
        })
      ]);
    } finally {
      clearTimeout(timer);
    }
  } catch {
    return false;
  }
}

export function supportsWebRTCH265() {
  if (!window.RTCRtpReceiver?.getCapabilities) return false;

  try {
    return (
      window.RTCRtpReceiver.getCapabilities('video')?.codecs.some(
        ({ mimeType }) => mimeType.toLowerCase() === 'video/h265'
      ) ?? false
    );
  } catch {
    return false;
  }
}
