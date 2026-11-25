import type { ControlRegionConfig } from '@/types';
import { ControlRegionMode, InputRegion, OriginalResolution } from '@/types';
import { http } from '@/lib/http.ts';
import { inputLeaseHeaders } from '@/lib/input-lease.ts';
import type { OledLayout } from '@/lib/oled/layout.ts';
import type { ScreenTextResult } from '@/lib/screen-text';
import type { TlsStatus } from '@/lib/tls-certificate';
import type { VideoCapabilities, VideoSettingsBody } from '@/lib/video-model';

// get NanoKVM information
export function getInfo() {
  return http.get<DeviceInfo>('/api/vm/info');
}

// get hardware information ({version, atx})
export function getHardware() {
  return http.get<{ version: string; atx?: boolean }>('/api/vm/hardware');
}

// The ATX switch (System > Device).
export type ATXState = {
  enabled: boolean;
  // The board has known ATX lines.
  available: boolean;
  // The board's default: on for PCIe, off for Cube and Lite.
  default: boolean;
  board: string;
};

export function getATX() {
  return http.get<ATXState>('/api/vm/atx');
}

export function setATX(enabled: boolean) {
  return http.post<{ enabled: boolean }>('/api/vm/atx', { enabled });
}

export function getDiagnostics() {
  return http.get('/api/vm/diagnostics');
}

export function downloadDiagnosticsReport() {
  return http.request({ method: 'get', url: '/api/vm/diagnostics/report', responseType: 'blob' });
}

// The developer archive takes up to a minute and a half to collect.
export function downloadDevArchive() {
  return http.request({
    method: 'get',
    url: '/api/vm/diagnostics/archive',
    responseType: 'blob',
    timeout: 120000
  });
}

// set gpio value
export function setGpio(type: string, duration: number) {
  const data = {
    type,
    duration
  };
  return http.post('/api/vm/gpio', data, { headers: inputLeaseHeaders() });
}

// Read the text on the screen with the TPU add-on: one recognition takes a
// few seconds, up to 90 on the server. Aborting the request stops it there.
// crop, fractions of the picture, reads only that area (at full size).
// Without a language the server reads in Latin.
export function getScreenText(
  language: string | undefined,
  signal?: AbortSignal,
  crop?: { left: number; top: number; width: number; height: number }
) {
  return http.request<ScreenTextResult>({
    method: 'get',
    url: '/api/vm/screen/text',
    params: {
      ...(language && { lang: language }),
      ...(crop && {
        crop: [crop.left, crop.top, crop.width, crop.height]
          .map((v) => Math.min(1, Math.max(0, v)).toFixed(4))
          .join(',')
      })
    },
    timeout: 100 * 1000,
    signal
  });
}

export type ScreenTextSettings = {
  // Text recognition is on: the toolbar button, MCP and PicoClaw read.
  enabled: boolean;
  // The models of at least one language are installed, so it can read.
  installed: boolean;
  languages: { name: string; installed: boolean }[];
  // A language change waits or runs on the device: the languages may still
  // change.
  changing: boolean;
};

export function getScreenTextSettings() {
  return http.get<ScreenTextSettings>('/api/vm/screen/text/settings');
}

export function setScreenTextEnabled(enabled: boolean) {
  return http.post<ScreenTextSettings>('/api/vm/screen/text/settings', { enabled });
}

// Downloads or removes the models of a language: a download from the NanoKVM
// add-on repository can take minutes on a slow link. The server gives apk up
// to 30 minutes; the page waits a little longer, so that it usually hears the
// server's answer instead of giving up first. An answer can still be lost
// (a change started in another browser tab runs first, or the connection
// drops), so after a lost answer the page reads the settings until they
// report no language change running.
export function setScreenTextLanguage(language: string, installed: boolean) {
  return http.post<ScreenTextSettings>(
    '/api/vm/screen/text/language',
    { language, installed },
    { timeout: 35 * 60 * 1000 }
  );
}

// update screen arguments
export function getScreen() {
  return http.get<ScreenStatus>('/api/vm/screen');
}

export function updateScreen(type: string, value: number, confirmPowerCycle = false) {
  const data = {
    type,
    value,
    confirmPowerCycle
  };
  return http.post('/api/vm/screen', data);
}

// every video choice this device offers, and why the others are unavailable
export function getVideoCapabilities() {
  return http.get<VideoCapabilities>('/api/vm/video/capabilities');
}

// apply several video settings together; returns the new capabilities
export function applyVideoSettings(settings: VideoSettingsBody, confirmPowerCycle = false) {
  return http.post<VideoCapabilities>('/api/vm/video', { ...settings, confirmPowerCycle });
}

// The watermark drawn into every stream: blocks, each a PNG placed at x, y
// in thousandths of the free room inside the margin (0 left or top, 500
// centre, 1000 right or bottom). design is the editor's own record.
export type WatermarkState = {
  enabled: boolean;
  margin: number;
  design: string;
  // image: the block's PNG, base64; empty if missing.
  blocks: { x: number; y: number; image: string }[];
  // Blocks are drawn for a refWidth-wide frame, each at most maxWidth x
  // maxHeight, together at most maxArea pixels.
  refWidth: number;
  maxWidth: number;
  maxHeight: number;
  maxArea: number;
  maxBlocks: number;
  // PNG bytes of one block.
  maxBytes: number;
  // Blocks the device could not put on a running output (it tries again).
  missing: number;
};

export function getWatermark() {
  return http.get<WatermarkState>('/api/vm/video/watermark');
}

// A block's image (base64 PNG) replaces its picture; without it the block
// keeps the saved picture of the same index.
export function setWatermark(settings: {
  enabled: boolean;
  margin: number;
  design: string;
  blocks: { x: number; y: number; image?: string }[];
}) {
  return http.post<WatermarkState>('/api/vm/video/watermark', settings);
}

// get the device-level absolute mouse input region
export function getInputRegion() {
  return http.get<ControlRegionConfig>('/api/vm/input-region');
}

export function setInputRegionConfig(
  region: InputRegion,
  regions: InputRegion[],
  selectedRegion: string
) {
  return http.post('/api/vm/input-region', {
    mode: 'manual',
    ...region,
    regions,
    selectedRegion,
    selectedResolution: ''
  });
}

export function setControlRegionMode(mode: ControlRegionMode) {
  return http.post('/api/vm/input-region', { mode });
}

export function setOriginalResolutionConfig(
  resolutions: OriginalResolution[],
  selectedResolution: string,
  selectedRegion: string
) {
  return http.post('/api/vm/input-region', {
    mode: 'manual',
    resolutions,
    selectedResolution,
    selectedRegion
  });
}

export function setSelectedOriginalResolution(selectedResolution: string) {
  return http.post('/api/vm/input-region', {
    mode: 'manual',
    selectedResolution,
    selectedRegion: ''
  });
}

export function setManualRegions(
  regions: InputRegion[],
  selectedRegion: string,
  selectedResolution = ''
) {
  return http.post('/api/vm/input-region', {
    mode: 'manual',
    regions,
    selectedRegion,
    selectedResolution
  });
}

// get OLED configuration
export function getOLED() {
  return http.get<{ exist: boolean; sleep: number }>('/api/vm/oled');
}

// set OLED configuration
export function setOLED(sleep: number) {
  return http.post('/api/vm/oled', { sleep });
}

// The OLED main page: the panel (empty without an OLED), its built-in
// layout and the saved one, if any.
export function getOLEDLayout() {
  return http.get<{ panel: string; builtIn: OledLayout | null; saved: OledLayout | null }>(
    '/api/vm/oled/layout'
  );
}

export function setOLEDLayout(layout: OledLayout) {
  return http.post('/api/vm/oled/layout', { layout });
}

// Back to the built-in layout.
export function deleteOLEDLayout() {
  return http.delete('/api/vm/oled/layout');
}

// Shown on the panel for 15 s: send it again to keep it there.
export function setOLEDPreview(layout: OledLayout) {
  return http.post('/api/vm/oled/preview', { layout });
}

export function deleteOLEDPreview() {
  return http.delete('/api/vm/oled/preview');
}

// reset HDMI
export function resetHdmi() {
  return http.post('/api/vm/hdmi/reset');
}

// get HDMI state
export function getHdmiState() {
  return http.get<{ viewerCount: number; enabled: boolean; signal: boolean; idleTimeout: number }>(
    '/api/vm/hdmi'
  );
}

// enable HDMI
function enableHdmi() {
  return http.post('/api/vm/hdmi/enable');
}

// disable HDMI
function disableHdmi() {
  return http.post('/api/vm/hdmi/disable');
}

// set HDMI state
export function setHdmiState(enabled: boolean) {
  if (enabled) {
    return enableHdmi();
  }
  return disableHdmi();
}

// set HDMI idle timeout in minutes
export function setHdmiIdleTimeout(minutes: number) {
  return http.post('/api/vm/hdmi/timeout', { minutes });
}

// get SSH state
export function getSSHState() {
  return http.get<{ enabled: boolean; rescue: boolean }>('/api/vm/ssh');
}

// enable SSH
export function enableSSH(password: string) {
  return http.post(
    '/api/vm/ssh/enable',
    { password, passwordEncoding: 'plain' },
    { sensitive: true }
  );
}

// disable SSH
export function disableSSH() {
  return http.post('/api/vm/ssh/disable');
}

// get mouse jiggler
export function getMouseJiggler() {
  return http.get<{ enabled: boolean; mode: 'absolute' | 'relative' }>('/api/vm/mouse-jiggler');
}

// set mouse jiggler
export function setMouseJiggler(enabled: boolean, mode: string) {
  // Matches the server route exactly; the slash-less form relied on a redirect.
  return http.post('/api/vm/mouse-jiggler/', { enabled, mode });
}

// get Hostname
export function getHostname() {
  return http.get<{ hostname: string }>('/api/vm/hostname');
}

// set Hostname
export function setHostname(hostname: string) {
  return http.post('/api/vm/hostname', { hostname });
}

// get WebTitle
export type ScreenPreview = {
  state: 'ok' | 'disabled' | 'no-signal' | 'unavailable';
  jpeg?: string; // base64
  width?: number;
  height?: number;
};

// A small still picture of the host screen; it starts no video session.
export function getScreenPreview() {
  return http.get<ScreenPreview>('/api/vm/screen/preview');
}

export function getWebTitle() {
  return http.get<{ title: string }>('/api/vm/web-title');
}

// set WebTitle
export function setWebTitle(title: string) {
  return http.post('/api/vm/web-title', { title });
}

// get mDNS state
export function getMdnsState() {
  return http.get<{ enabled: boolean }>('/api/vm/mdns');
}

// enable mDNS
export function enableMdns() {
  return http.post('/api/vm/mdns/enable');
}

// disable mDNS
export function disableMdns() {
  return http.post('/api/vm/mdns/disable');
}

export type BluetoothState = {
  available: boolean;
  enabled: boolean;
  loaded: boolean;
  version: string;
};

// get Bluetooth state and version
export function getBluetoothState() {
  return http.get<BluetoothState>('/api/vm/bluetooth');
}

// load Bluetooth now and at every boot
export function enableBluetooth() {
  return http.post('/api/vm/bluetooth/enable');
}

// unload Bluetooth and keep it off at boot
export function disableBluetooth() {
  return http.post('/api/vm/bluetooth/disable');
}

// enable / disable TLS
export function setTLS(enabled: boolean) {
  return http.post('/api/vm/tls', { enabled });
}

// HTTPS and certificate status
export function getTLS() {
  return http.get<TlsStatus>('/api/vm/tls');
}

// install a custom certificate: form field "cert" (PEM, chain and key
// allowed in one text) and optionally "key"
export function uploadTLSCertificate(formData: FormData) {
  return http.request({
    url: '/api/vm/tls/certificate',
    method: 'post',
    headers: {
      'Content-Type': 'multipart/form-data'
    },
    data: formData,
    sensitive: true
  });
}

// back to the generated certificate
export function deleteTLSCertificate() {
  return http.post('/api/vm/tls/certificate/delete');
}

// serve the certificate pair as it is on disk now
export function reloadTLS() {
  return http.post('/api/vm/tls/reload');
}

// reboot
export function reboot() {
  return http.post('/api/vm/system/reboot');
}

export type MemorySwap = {
  enabled: boolean;
  available: boolean;
  sizeMiB: number;
  usedBytes: number;
  priority: number;
  memoryBytes?: number;
  compressedBytes?: number;
  originalBytes?: number;
  algorithm?: string;
  auto?: boolean;
  autoMiB?: number;
};

export type MemoryStatus = {
  // The booted video memory. shared: one reusable region for every resolution
  // that Linux uses while video does not; false only until a device updated
  // from a fixed carveout restarts.
  videoMemory: {
    sizeMiB: number;
    shared: boolean;
  };
  totalBytes: number;
  availableBytes: number;
  usedBytes: number;
  cachedBytes: number;
  swapTotalBytes: number;
  swapUsedBytes: number;
  videoBytes: number;
  zram: MemorySwap;
  sd: MemorySwap;
};

export function getMemoryStatus() {
  return http.get<MemoryStatus>('/api/vm/memory/status');
}

export function setMemorySwap(kind: 'zram' | 'sd', enabled: boolean, sizeMiB: number) {
  return http.post<MemoryStatus>('/api/vm/memory/swap', { kind, enabled, sizeMiB });
}

export function getCPUFrequency() {
  return http.get<CPUFrequency>('/api/vm/cpu-frequency');
}
export function setCPUFrequency(target: number, applyAtBoot: boolean) {
  return http.post('/api/vm/cpu-frequency', { target, applyAtBoot });
}

export function importSSHKeys(keys: string, password: string) {
  return http.post('/api/vm/ssh/keys/import', { keys, password }, { sensitive: true });
}

type DeviceInfo = {
  ips: { type: string; addr: string }[];
  mdns: string;
  application: string;
  deviceKey: string;
};

export type ScreenStatus = {
  width: number;
  height: number;
  fps: number;
  quality: number;
  bitRate: number;
  mjpegChroma: number;
  mjpegChromaActive: number;
  mjpegChromaFallback: string;
  // The straight-from-capture setting (both codecs) and the codecs it applies to.
  hevcDirect: number;
  hevcDirectSupported: boolean;
  viDirectH264Supported?: boolean;
  monitor: number;
  portrait: boolean;
  portraitResolution: number;
  inputWidth: number;
  inputHeight: number;
  outputWidth: number;
  outputHeight: number;
  mjpegOutputWidth: number;
  mjpegOutputHeight: number;
  videoOutputWidth: number;
  videoOutputHeight: number;
  effectiveFps: number;
  // The main stream: H.264/H.265 while it runs, else MJPEG.
  measuredFps: number;
  // The MJPEG stream alone (older servers: absent).
  mjpegMeasuredFps?: number;
  measuredKbps: number;
  // The bit rate the stream is held to below bitRate while every viewer lags, kbit/s; 0 when it uses bitRate (older
  // servers: absent).
  bitRateCap?: number;
  // Where the H.264/H.265 encoder takes its frames (kvm_mmf.hpp): 0 no video
  // encoder, 1 straight from capture, 2-8 through VPSS and why (older
  // servers: absent).
  videoPath?: number;
  // Device-wide MJPEG frame detection, saved by the server.
  frameDetect: boolean;
};
type CPUFrequency = {
  supported: boolean;
  throttled?: boolean;
  running: number;
  target: number;
  options: number[];
  applyAtBoot?: boolean;
  bootFallback?: boolean;
};
