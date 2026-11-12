import { http } from '@/lib/http';

export type RustDeskConfig = {
  service_enabled: boolean;
  audio_enabled?: boolean;
  local_pointer?: boolean;
  // The direct listener: 127.0.0.1 unless connecting by address is on.
  listen_address?: string;
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
  supports_audio_settings?: boolean;
  supports_pointer_settings?: boolean;
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
  http.request<RustDeskStatus>({ method: 'get', url: '/api/addons/rustdesk/status', signal });
export const configureRustDesk = (data: RustDeskConfig) =>
  http.request<null>({
    method: 'put',
    url: '/api/addons/rustdesk/config',
    data,
    timeout: 190000,
    sensitive: true
  });
export const rustDeskPackageAction = (
  action: 'install' | 'upgrade' | 'remove' | 'check' | 'regenerate-password'
) => http.post<null>('/api/addons/rustdesk/' + action, undefined, { timeout: 190000 });
