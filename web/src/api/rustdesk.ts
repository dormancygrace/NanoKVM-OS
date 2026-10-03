import { http } from '@/lib/http';

export type RustDeskConfig = {
  service_enabled: boolean;
  use_official_id_server: boolean;
  rendezvous_server: string;
  relay_server: string;
  server_key: string;
  password?: string;
  codec: 'h264' | 'h265';
  max_clients: number;
};
export type RustDeskStatus = {
  installed: boolean;
  available: boolean;
  running: boolean;
  config: RustDeskConfig;
  has_password: boolean;
  id: string;
  runtime?: { registered: boolean; sessions: number };
};
export const getRustDeskStatus = () => http.get('/api/addons/rustdesk/status');
export const configureRustDesk = (data: RustDeskConfig) =>
  http.request({ method: 'put', url: '/api/addons/rustdesk/config', data, timeout: 190000 });
export const rustDeskPackageAction = (action: 'install' | 'upgrade' | 'remove') =>
  http.post('/api/addons/rustdesk/' + action, undefined, { timeout: 190000 });
