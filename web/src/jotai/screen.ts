import { atom } from 'jotai';

import type { ScreenTextSettings } from '@/api/vm.ts';
import { ControlRegionMode, InputRegion, Resolution } from '@/types';
import type { ScreenshotSource } from '@/lib/screenshot.ts';

export const isHdmiEnabledAtom = atom(false);
export const captureReadyAtom = atom(false);
export const captureBusyAtom = atom(false);
export const screenshotSourceAtom = atom<ScreenshotSource | null>(null);
// The recorder is busy (choosing a file, recording or saving): the stream it
// records must keep running while the tab is hidden.
export const screenRecordingAtom = atom(false);

// Active video subscriptions, not browser control sockets or screenshot leases.
export const videoSessionCountAtom = atom<number | null>(null);

// video mode
// direct: stream H.264 over HTTP
// h264: stream H.264 over WebRTC
// mjpeg: stream JPEG over HTTP
export const videoModeAtom = atom('');

export const videoScaleAtom = atom<number>(1.0);

// browser screen resolution
export const resolutionAtom = atom<Resolution | null>(null);

// currently effective absolute mouse input region
export const inputRegionAtom = atom<InputRegion | null>(null);
export const manualRegionsAtom = atom<InputRegion[]>([]);
export const selectedManualRegionAtom = atom<string>('');
export const selectedOriginalResolutionAtom = atom<string>('');

// device-level control region mode; disabled by default
export const controlRegionModeAtom = atom<ControlRegionMode>('off');

// show the live input-region selection overlay
export const inputRegionSelectingAtom = atom(false);

// show the overlay that reads text on the screen (TPU)
export const screenTextOpenAtom = atom(false);

// Settings > AI > Text recognition as the device last answered: the toolbar
// button shows while a language is installed and recognition is on, and
// follows the page's changes at once. null until loaded.
export const screenTextSettingsAtom = atom<ScreenTextSettings | null>(null);

// The change of those settings on its way to the device: 'switch', or the
// language being installed or removed; '' when none. Kept here, not in the
// page, so that a download still running after the page was left and opened
// again keeps the other controls waiting.
export const screenTextChangeAtom = atom('');

export const streamFpsAtom = atom(60);
