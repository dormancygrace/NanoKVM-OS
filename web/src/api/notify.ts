import { http } from '@/lib/http';

export type NotifyKind = 'power' | 'hdmi' | 'security';

export type NotifyField = {
  key: string;
  kind: 'text' | 'url' | 'email' | 'number' | 'select' | 'bool';
  secret?: boolean;
  required?: boolean;
  options?: string[];
  default?: string;
  placeholder?: string;
};

export type NotifyChannelType = {
  type: string;
  fields: NotifyField[];
  image: boolean;
};

export type NotifyChannel = {
  id: string;
  type: string;
  name: string;
  enabled: boolean;
  events: NotifyKind[];
  // Without the secret fields: secretsSet names the stored ones.
  settings: Record<string, string>;
  secretsSet: string[];
};

export type NotifyStatus = {
  language: string;
  languages: string[];
  channels: NotifyChannel[];
  types: NotifyChannelType[];
  events: NotifyKind[];
};

// A channel as the page sends it: an empty secret keeps the stored value,
// clear names the secrets to remove.
export type NotifyChannelUpdate = Omit<NotifyChannel, 'secretsSet'> & { clear: string[] };

export const getNotifyConfig = () =>
  http.request<NotifyStatus>({ method: 'get', url: '/api/notify/config', timeout: 10000 });

export const saveNotifyConfig = (data: { language: string; channels: NotifyChannelUpdate[] }) =>
  http.request<NotifyStatus>({
    method: 'put',
    url: '/api/notify/config',
    data,
    timeout: 15000,
    sensitive: true
  });

export const testNotifyChannel = (data: NotifyChannelUpdate) =>
  http.request<null>({
    method: 'post',
    url: '/api/notify/test',
    data,
    timeout: 40000,
    sensitive: true
  });
