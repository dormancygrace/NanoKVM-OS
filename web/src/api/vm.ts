import { ControlRegionMode, InputRegion, OriginalResolution } from '@/types';
import { http } from '@/lib/http.ts';
import { inputLeaseHeaders } from '@/lib/input-lease.ts';

// get NanoKVM information
export function getInfo() {
  return http.get('/api/vm/info');
}

// get hardware information
export function getHardware() {
  return http.get('/api/vm/hardware');
}

export function getDiagnostics() {
  return http.get('/api/vm/diagnostics');
}

export function downloadDiagnosticsReport() {
  return http.request({ method: 'get', url: '/api/vm/diagnostics/report', responseType: 'blob' });
}

// set gpio value
export function setGpio(type: string, duration: number) {
  const data = {
    type,
    duration
  };
  return http.post('/api/vm/gpio', data, { headers: inputLeaseHeaders() });
}

// get gpio value
export function getGpio() {
  return http.get('/api/vm/gpio');
}

// Read the text on the screen with the TPU add-on: one recognition takes a
// few seconds, up to 90 on the server. Aborting the request stops it there.
// crop, fractions of the picture, reads only that area (at full size).
// Without a language the server reads in the device's default.
export function getScreenText(
  language: string | undefined,
  signal?: AbortSignal,
  crop?: { left: number; top: number; width: number; height: number }
) {
  return http.request({
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
  // The language a read uses unless one is picked for it.
  language: string;
  languages: { name: string; installed: boolean }[];
};

export function getScreenTextSettings() {
  return http.get('/api/vm/screen/text/settings');
}

export function setScreenTextLanguage(language: string) {
  return http.post('/api/vm/screen/text/settings', { language });
}

// update screen arguments
export function getScreen() {
  return http.get('/api/vm/screen');
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
  return http.get('/api/vm/video/capabilities');
}

// apply several video settings together; returns the new capabilities
export function applyVideoSettings(
  settings: Record<string, number | boolean | string>,
  confirmPowerCycle = false
) {
  return http.post('/api/vm/video', { ...settings, confirmPowerCycle });
}

// get the device-level absolute mouse input region
export function getInputRegion() {
  return http.get('/api/vm/input-region');
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
  return http.get('/api/vm/oled');
}

// set OLED configuration
export function setOLED(sleep: number) {
  return http.post('/api/vm/oled', { sleep });
}

// reset HDMI
export function resetHdmi() {
  return http.post('/api/vm/hdmi/reset');
}

// get HDMI state
export function getHdmiState() {
  return http.get('/api/vm/hdmi');
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
  return http.get('/api/vm/ssh');
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
  return http.get('/api/vm/mouse-jiggler');
}

// set mouse jiggler
export function setMouseJiggler(enabled: boolean, mode: string) {
  // Matches the server route exactly; the slash-less form relied on a redirect.
  return http.post('/api/vm/mouse-jiggler/', { enabled, mode });
}

// get Hostname
export function getHostname() {
  return http.get('/api/vm/hostname');
}

// set Hostname
export function setHostname(hostname: string) {
  return http.post('/api/vm/hostname', { hostname });
}

// get WebTitle
export function getWebTitle() {
  return http.get('/api/vm/web-title');
}

// set WebTitle
export function setWebTitle(title: string) {
  return http.post('/api/vm/web-title', { title });
}

// get mDNS state
export function getMdnsState() {
  return http.get('/api/vm/mdns');
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
  return http.get('/api/vm/bluetooth');
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
  return http.get('/api/vm/tls');
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
    data: formData
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
  recompress?: boolean;
  recompressAvailable?: boolean;
  recompressReady?: boolean;
  auto?: boolean;
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
  return http.get('/api/vm/memory/status');
}

export function setMemorySwap(
  kind: 'zram' | 'sd',
  enabled: boolean,
  sizeMiB: number,
  recompress?: boolean
) {
  return http.post('/api/vm/memory/swap', { kind, enabled, sizeMiB, recompress });
}

export function getCPUFrequency() {
  return http.get('/api/vm/cpu-frequency');
}
export function setCPUFrequency(target: number, applyAtBoot: boolean) {
  return http.post('/api/vm/cpu-frequency', { target, applyAtBoot });
}

// MiB; current and default are 0 without a limit.
export type MemoryLimit = {
  enabled: boolean;
  limit: number;
  default: number;
  current: number;
  minimum: number;
};

// The Go memory limit of the NanoKVM server.
export function getMemoryLimit() {
  return http.get('/api/vm/memory/limit');
}

// enabled false removes the saved limit and returns to the default.
export function setMemoryLimit(enabled: boolean, limit: number) {
  return http.post('/api/vm/memory/limit', { enabled, limit });
}

export function importSSHKeys(keys: string, password: string) {
  return http.post('/api/vm/ssh/keys/import', { keys, password });
}
