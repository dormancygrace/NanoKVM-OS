import { http } from '@/lib/http';

export type RustDeskConfig = {
  service_enabled: boolean;
  webrtc_enabled?: boolean;
  use_official_id_server: boolean;
  rendezvous_server: string;
  relay_server: string;
  server_key: string;
  password?: string;
  password_mode: 'temporary' | 'permanent';
  codec?: 'auto' | 'h264' | 'h265';
  max_clients: number;
};
export type RustDeskStatus = {
  installed: boolean;
  supports_transport_settings?: boolean;
  supports_audio?: boolean;
  usb_audio_enabled?: boolean;
  version?: string;
  rustdesk_version?: string;
  source_url?: string;
  update_version?: string;
  available: boolean;
  running: boolean;
  config: RustDeskConfig;
  has_password: boolean;
  temporary_password?: string;
  id: string;
  runtime?: { registered: boolean; sessions: number };
};
export const getRustDeskStatus = (signal?: AbortSignal) =>
  http.request({ method: 'get', url: '/api/addons/rustdesk/status', signal });
export const configureRustDesk = (data: RustDeskConfig) =>
  http.request({ method: 'put', url: '/api/addons/rustdesk/config', data, timeout: 190000 });
export const rustDeskPackageAction = (
  action: 'install' | 'upgrade' | 'remove' | 'regenerate-password'
) => http.post('/api/addons/rustdesk/' + action, undefined, { timeout: 190000 });
