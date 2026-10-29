import { useEffect, useState } from 'react';
import { Alert, Button, message, Switch } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { getScreenTextSettings, setScreenTextEnabled, type ScreenTextSettings } from '@/api/vm.ts';
import { addonInventoryAtom } from '@/jotai/addons.ts';
import { Panel, SettingRow } from '@/components/ui/settings.tsx';

// Settings > AI > Text recognition: reading the text on the screen with the
// TPU add-on, on or off for the toolbar button, MCP and PicoClaw together.
// Each read picks its language (the reading dialog remembers the last one).
export const ScreenTextSettingsPage = ({ onOpenAddons }: { onOpenAddons: () => void }) => {
  const { t } = useTranslation();
  const [inventory, setInventory] = useAtom(addonInventoryAtom);
  const [settings, setSettings] = useState<ScreenTextSettings>();
  const [busy, setBusy] = useState(false);
  const installed = inventory?.tpu?.installed;

  useEffect(() => {
    if (!installed) return;
    let active = true;
    getScreenTextSettings()
      .then((rsp) => {
        if (active && rsp.code === 0) setSettings(rsp.data as ScreenTextSettings);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [installed]);

  async function toggle(enabled: boolean) {
    if (!settings) return;
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
      <Alert
        type="info"
        showIcon
        message={t('settings.ai.screenText.notInstalled')}
        action={
          <Button size="small" onClick={onOpenAddons}>
            {t('settings.ai.screenText.openAddons')}
          </Button>
        }
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
          disabled={!settings || busy}
          onChange={(enabled) => void toggle(enabled)}
        />
      </SettingRow>
    </Panel>
  );
};
