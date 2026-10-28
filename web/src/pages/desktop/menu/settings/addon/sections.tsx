import { useAtomValue } from 'jotai';

import { addonInventoryAtom } from '@/jotai/addons';

import { AddonPage } from './index';

// The sections add-ons add to the core page host (settings.section of their
// manifests, server/service/addons): each at the end, in the order of ids.
export const AddonSections = ({ host }: { host: string }) => {
  const inventory = useAtomValue(addonInventoryAtom);
  const manifests = (inventory?.addons ?? []).filter(
    (manifest) => manifest.settings?.section === host
  );
  return (
    <>
      {manifests.map((manifest) => (
        <AddonPage key={manifest.id} manifest={manifest} embedded />
      ))}
    </>
  );
};
