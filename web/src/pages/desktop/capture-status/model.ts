export const CAPTURE_STATUS_EVENT = 'capture-status';

export type CaptureStatus = {
  ok: boolean;
  result: number;
  message: string;
  mode: string;
  severity: 'error' | 'warning' | '';
  updatedAt: string;
  // With -6: an input refused for the video memory of this boot, on a device
  // that has not restarted since the update to the shared video memory.
  inputWidth?: number;
  inputHeight?: number;
  videoMemory?: boolean;
};

export function parseCaptureStatusMessage(message: { data: unknown }): CaptureStatus | null {
  if (typeof message.data !== 'string') {
    return null;
  }

  try {
    const envelope = JSON.parse(message.data) as { data?: unknown };
    if (typeof envelope.data !== 'string') {
      return null;
    }

    const status = JSON.parse(envelope.data) as Partial<CaptureStatus>;
    if (typeof status.ok !== 'boolean' || typeof status.result !== 'number') {
      return null;
    }

    return {
      ok: status.ok,
      result: status.result,
      message: typeof status.message === 'string' ? status.message : '',
      mode: typeof status.mode === 'string' ? status.mode : '',
      severity: status.severity === 'warning' ? 'warning' : status.ok ? '' : 'error',
      updatedAt: typeof status.updatedAt === 'string' ? status.updatedAt : '',
      ...rejectedInput(status)
    };
  } catch (error) {
    console.log(error);
    return null;
  }
}

function rejectedInput(status: Partial<CaptureStatus>) {
  const { inputWidth, inputHeight, videoMemory } = status;
  if (
    videoMemory !== true ||
    typeof inputWidth !== 'number' ||
    typeof inputHeight !== 'number' ||
    inputWidth <= 0 ||
    inputHeight <= 0
  ) {
    return {};
  }
  return { inputWidth, inputHeight, videoMemory };
}

// An input refused for the video memory: its size.
export function getVideoMemoryNeed(status: CaptureStatus | null) {
  if (!status || status.ok || status.result !== -6 || !status.videoMemory) {
    return null;
  }
  return { size: `${status.inputWidth} × ${status.inputHeight}` };
}

export function getCaptureStatusMessageKey(result: number) {
  switch (result) {
    case -8:
      return 'screen.captureStatus.mjpeg4k';
    case -7:
      return 'screen.captureStatus.hdmiError';
    case -6:
      return 'screen.captureStatus.unsupportedResolution';
    case -5:
      return 'screen.captureStatus.retrieving';
    case -4:
      return 'screen.captureStatus.changingResolution';
    case -3:
      return 'screen.captureStatus.updateFailed';
    case -2:
      return 'screen.captureStatus.videoError';
    case -1:
      return 'screen.captureStatus.noHdmi';
    default:
      return result < 0 ? 'screen.captureStatus.unavailable' : '';
  }
}

export function shouldAcceptCaptureStatus(current: CaptureStatus | null, next: CaptureStatus) {
  if (!current) {
    return true;
  }

  return getCaptureStatusTimestamp(next) >= getCaptureStatusTimestamp(current);
}

function getCaptureStatusTimestamp(status: CaptureStatus) {
  const timestamp = Date.parse(status.updatedAt);
  return Number.isFinite(timestamp) ? timestamp : 0;
}
