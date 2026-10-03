import { http } from '@/lib/http';

export type AddonInventory = {
  rustdesk: { installed: boolean };
  picoclaw: { installed: boolean };
};

export const getAddonInventory = () =>
  http.request({ method: 'get', url: '/api/addons/inventory', timeout: 5000 });
