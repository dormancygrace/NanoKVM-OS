import { useEffect, useState } from 'react';
import { Alert, Button } from 'antd';
import { useSetAtom } from 'jotai';
import { DownloadIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  addonCatalogAction,
  getAddonCatalog,
  getAddonInventory,
  type AddonCatalog,
  type AddonInventory
} from '@/api/addons.ts';
import { addonInventoryAtom } from '@/jotai/addons.ts';

import { MinOsNotice } from './catalog-addon';

// The notice of a page whose add-on is not installed, with its Install button
// when the repository offers the add-on; otherwise (no list yet, not offered)
// a link to Software > Add-ons. Once installed the inventory turns the page on.
export const CatalogInstallNotice = ({
  name,
  message,
  onOpenAddons
}: {
  name: string;
  message: string;
  onOpenAddons: () => void;
}) => {
  const { t } = useTranslation();
  const setInventory = useSetAtom(addonInventoryAtom);
  const [catalog, setCatalog] = useState<AddonCatalog>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    let active = true;
    getAddonCatalog()
      .then((response) => {
        if (active && response.code === 0) setCatalog(response.data as AddonCatalog);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, []);

  const addon = catalog?.addons.find((entry) => entry.name === name);

  async function install() {
    if (busy) return;
    setBusy(true);
    try {
      const response = await addonCatalogAction('install', name);
      if (response.code !== 0) throw new Error(response.msg);
      setError('');
      const inventory = await getAddonInventory();
      if (inventory.code === 0) setInventory(inventory.data as AddonInventory);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('settings.software.addons.failed'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-3">
      <Alert
        type="info"
        showIcon
        message={message}
        action={
          addon?.version ? (
            <Button
              size="small"
              type="primary"
              icon={<DownloadIcon size={14} />}
              loading={busy}
              disabled={!addon.compatible}
              onClick={() => void install()}
            >
              {t('settings.software.addons.install')}
            </Button>
          ) : (
            <Button size="small" onClick={onOpenAddons}>
              {t('settings.ai.screenText.openAddons')}
            </Button>
          )
        }
      />
      <MinOsNotice addon={addon} os={catalog?.os} />
      {error && <Alert type="error" showIcon message={error} />}
    </div>
  );
};
