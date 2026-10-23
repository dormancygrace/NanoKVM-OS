import { http } from '@/lib/http';

export type AddonInventory = {
  rustdesk: { installed: boolean };
  picoclaw: { installed: boolean };
  ipmi: { installed: boolean };
};

export const getAddonInventory = () =>
  http.request({ method: 'get', url: '/api/addons/inventory', timeout: 5000 });

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
  http.request({ method: 'get', url: '/api/addons/catalog', timeout: 10000 });
export const checkAddonCatalog = () =>
  http.post('/api/addons/catalog/check', undefined, { timeout: 10000 });
export const addonCatalogAction = (action: 'install' | 'remove', name: string) =>
  http.post('/api/addons/catalog/' + action, { name }, { timeout: 190000 });
