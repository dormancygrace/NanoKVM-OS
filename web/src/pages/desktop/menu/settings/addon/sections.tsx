import { useAtomValue } from 'jotai';

import type { AddonManifest } from '@/api/addons.ts';
import { addonInventoryAtom } from '@/jotai/addons';

// The sections add-ons add to the core page host (settings.section of their
// manifests, server/service/addons), in the order of ids. The host page
// decides where each one goes (BmcSettingsPage: a tab).
export const useAddonSections = (host: string): AddonManifest[] => {
  const inventory = useAtomValue(addonInventoryAtom);
  return (inventory?.addons ?? []).filter((manifest) => manifest.settings?.section === host);
};
