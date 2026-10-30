import { http } from '@/lib/http';

export type AddonInventory = {
  rustdesk: { installed: boolean };
  picoclaw: { installed: boolean };
  ipmi: { installed: boolean };
  // Absent from servers older than the TPU add-on.
  // enabled: text recognition is on (absent from older servers: on).
  tpu?: { installed: boolean; enabled?: boolean };
  // Add-ons that describe themselves (server/service/addons, docs/ADDONS.md).
  addons?: AddonManifest[];
};

// A text in several languages; "en" is always there.
export type AddonText = Record<string, string>;

export type AddonFieldType = 'text' | 'password' | 'number' | 'select' | 'switch' | 'status';

export type AddonField = {
  key: string;
  type: AddonFieldType;
  label: AddonText;
  help?: AddonText;
  options?: { value: string; label: AddonText }[];
  min?: number;
  max?: number;
  required?: boolean;
};

export type AddonAction = {
  id: string;
  label: AddonText;
  url: string;
  confirm?: AddonText;
  danger?: boolean;
};

export type AddonForm = {
  source: string;
  submit?: string;
  refresh?: number;
  fields: AddonField[];
  actions?: AddonAction[];
};

export type AddonManifest = {
  id: string;
  api: number;
  title: AddonText;
  description?: AddonText;
  backend?: { socket: string };
  // group: a page of its own; section: a section of that core page.
  settings?: { group?: string; section?: string; icon?: string; form: AddonForm };
};

export const getAddonInventory = () =>
  http.request<AddonInventory>({ method: 'get', url: '/api/addons/inventory', timeout: 5000 });

// An add-on of the NanoKVM repository, as of the last update check.
export type CatalogAddon = {
  name: string;
  // Empty: installed, but the repository no longer offers it.
  version?: string;
  description?: string;
  url?: string;
  license?: string;
  // The first NanoKVM OS release it works with.
  minOs?: string;
  installed?: string;
  compatible: boolean;
};

export type AddonCatalog = {
  // Unix seconds of the check that stored the list; absent before the first.
  checkedAt?: number;
  os?: string;
  addons: CatalogAddon[];
  state: string;
  noticeEnabled: boolean;
};

export const getAddonCatalog = () =>
  http.request<AddonCatalog>({ method: 'get', url: '/api/addons/catalog', timeout: 10000 });
export const checkAddonCatalog = () =>
  http.post<null>('/api/addons/catalog/check', undefined, { timeout: 10000 });
export const addonCatalogAction = (action: 'install' | 'remove', name: string) =>
  http.post<AddonCatalog>('/api/addons/catalog/' + action, { name }, { timeout: 190000 });
