import { http } from '@/lib/http';

export type NotifyKind = 'power' | 'hdmi' | 'security' | 'screen';

export type NotifyField = {
  key: string;
  // oauth: the stored sign-in; hidden: kept, not drawn.
  kind: 'text' | 'url' | 'email' | 'number' | 'select' | 'bool' | 'oauth' | 'hidden';
  secret?: boolean;
  required?: boolean;
  options?: string[];
  default?: string;
  placeholder?: string;
  // The field applies only while each named field has one of the values.
  show?: Record<string, string[]>;
  maxBytes?: number;
};

// A mail service the e-mail channel fills in; auth lists its sign-ins, the
// one to use first.
export type NotifyProvider = {
  id: string;
  host: string;
  port: string;
  security: string;
  auth: string[];
  oauth?: 'google' | 'microsoft';
  tenant?: string;
};

// A sign-in to show: a code to enter at url (device), or url to open and
// the address the browser ends on to paste back (code).
export type NotifySignIn = {
  id: string;
  kind: 'device' | 'code';
  url: string;
  userCode?: string;
  interval?: number;
  expiresIn: number;
};

// token is the sign-in's handle for the oauthToken field until it is saved.
export type NotifySignInResult = {
  pending: boolean;
  interval?: number;
  token?: string;
  account?: string;
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
  // The id of the channel's template; empty or absent: the standard one.
  template?: string;
  // The channel's sign-in failed: expired or revoked.
  authError?: string;
};

// A message's title and text, with {name} variables.
export type NotifyMessage = { title: string; body: string };

// A named template: its text for the messages it sets; an empty title or
// text is the standard one.
export type NotifyTemplate = {
  id: string;
  name: string;
  messages: Record<string, NotifyMessage>;
};

// A message a template can set, with its standard text.
export type NotifyMessageView = NotifyMessage & {
  key: string;
  kind: NotifyKind | 'test';
  vars: string[] | null;
};

export type NotifyStatus = {
  language: string;
  languages: string[];
  channels: NotifyChannel[];
  templates: NotifyTemplate[];
  messages: NotifyMessageView[];
  commonVars: string[];
  types: NotifyChannelType[];
  events: NotifyKind[];
  providers: NotifyProvider[];
};

// A channel as the page sends it: an empty secret keeps the stored value,
// clear names the secrets to remove. templateDraft is its template as it is
// on the page, for the Test button.
export type NotifyChannelUpdate = Omit<NotifyChannel, 'secretsSet' | 'authError'> & {
  clear: string[];
  templateDraft?: NotifyTemplate;
};

// One message with sample values; text has the device and time under it.
export type NotifyPreview = NotifyMessage & { key: string; text: string };

export const getNotifyConfig = () =>
  http.request<NotifyStatus>({ method: 'get', url: '/api/notify/config', timeout: 10000 });

export const previewNotify = (data: { language: string; template: NotifyTemplate | null }) =>
  http.request<NotifyPreview[]>({
    method: 'post',
    url: '/api/notify/preview',
    data,
    timeout: 10000
  });

export const saveNotifyConfig = (data: {
  language: string;
  channels: NotifyChannelUpdate[];
  templates: NotifyTemplate[];
}) =>
  http.request<NotifyStatus>({
    method: 'put',
    url: '/api/notify/config',
    data,
    timeout: 15000,
    sensitive: true
  });

export const startNotifySignIn = (data: NotifyChannelUpdate) =>
  http.request<NotifySignIn>({
    method: 'post',
    url: '/api/notify/oauth/start',
    data,
    timeout: 40000,
    sensitive: true
  });

export const finishNotifySignIn = (data: { id: string; code: string }) =>
  http.request<NotifySignInResult>({
    method: 'post',
    url: '/api/notify/oauth/finish',
    data,
    timeout: 40000,
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
