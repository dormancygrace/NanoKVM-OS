import { useEffect, useState } from 'react';
import { message, Switch } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { getScreenTextSettings, setScreenTextEnabled } from '@/api/vm.ts';
import { screenTextSwitch } from '@/lib/screen-text.ts';
import { addonInventoryAtom } from '@/jotai/addons.ts';
import { Panel, SettingRow } from '@/components/ui/settings.tsx';

import { CatalogInstallNotice } from '../software/catalog-install';

// Settings > AI > Text recognition: reading the text on the screen with the
// TPU add-on, on or off for the toolbar button, MCP and PicoClaw together.
// Each read picks its language (the reading dialog remembers the last one).
export const ScreenTextSettingsPage = ({ onOpenAddons }: { onOpenAddons: () => void }) => {
  const { t } = useTranslation();
  const [inventory, setInventory] = useAtom(addonInventoryAtom);
  const [settings, setSettings] = useState<ReturnType<typeof screenTextSwitch>>();
  const [busy, setBusy] = useState(false);
  const installed = inventory?.tpu?.installed;

  useEffect(() => {
    if (!installed) return;
    let active = true;
    getScreenTextSettings()
      .then((rsp) => {
        if (active && rsp.code === 0) setSettings(screenTextSwitch(rsp.data));
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [installed]);

  async function toggle(enabled: boolean) {
    if (!settings?.switchable) return;
    setBusy(true);
    try {
      const rsp = await setScreenTextEnabled(enabled);
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setSettings({ ...settings, enabled });
      // The toolbar button follows at once, not with the next inventory poll.
      if (inventory?.tpu) setInventory({ ...inventory, tpu: { ...inventory.tpu, enabled } });
    } catch {
      message.error(t('settings.ai.screenText.switchFailed'));
    } finally {
      setBusy(false);
    }
  }

  if (inventory && !installed) {
    return (
      <CatalogInstallNotice
        name="nanokvm-model-ocr-latin"
        message={t('settings.ai.screenText.notInstalled')}
        onOpenAddons={onOpenAddons}
      />
    );
  }

  return (
    <Panel className="space-y-4">
      <SettingRow
        label={t('settings.ai.screenText.enabled')}
        description={t('settings.ai.screenText.enabledDescription')}
        htmlFor="screen-text-enabled"
      >
        <Switch
          id="screen-text-enabled"
          aria-describedby="screen-text-enabled-description"
          checked={settings?.enabled ?? false}
          loading={!settings || busy}
          disabled={!settings?.switchable || busy}
          onChange={(enabled) => void toggle(enabled)}
        />
      </SettingRow>
    </Panel>
  );
};
