import { useEffect, useState } from 'react';
import { message, Select } from 'antd';
import { useAtomValue } from 'jotai';
import { useTranslation } from 'react-i18next';

import { getScreenTextSettings, setScreenTextLanguage, type ScreenTextSettings } from '@/api/vm.ts';
import { screenTextLanguages } from '@/lib/screen-text.ts';
import { addonInventoryAtom } from '@/jotai/addons.ts';
import { Panel, SettingRow } from '@/components/ui/settings.tsx';

import { CatalogInstallNotice } from '../software/catalog-install';

// Settings > AI > Text recognition: reading the text on the screen with the
// TPU add-on. The default language is the device's: the web dialog, MCP and
// PicoClaw reads all use it unless they pick another.
export const ScreenTextSettingsPage = ({ onOpenAddons }: { onOpenAddons: () => void }) => {
  const { t } = useTranslation();
  const inventory = useAtomValue(addonInventoryAtom);
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

  async function choose(language: string) {
    if (!settings) return;
    setBusy(true);
    try {
      const rsp = await setScreenTextLanguage(language);
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setSettings({ ...settings, language });
      message.success(t('settings.ai.screenText.saved'));
    } catch {
      message.error(t('settings.ai.screenText.saveFailed'));
    } finally {
      setBusy(false);
    }
  }

  if (inventory && !installed) {
    return (
      <CatalogInstallNotice
        name="nanokvm-tpu"
        message={t('settings.ai.screenText.notInstalled')}
        onOpenAddons={onOpenAddons}
      />
    );
  }

  return (
    <Panel className="space-y-4">
      <SettingRow
        label={t('settings.ai.screenText.language')}
        description={t('settings.ai.screenText.languageDescription')}
        htmlFor="screen-text-language"
      >
        <Select
          id="screen-text-language"
          aria-describedby="screen-text-language-description"
          value={settings?.language}
          loading={!settings || busy}
          disabled={!settings || busy}
          onChange={(language) => void choose(language)}
          popupMatchSelectWidth={false}
          style={{ minWidth: 240 }}
          options={screenTextLanguages.map((lang) => {
            const available = settings?.languages.find((l) => l.name === lang)?.installed ?? true;
            return {
              value: lang,
              disabled: !available,
              label: available
                ? t(`screenText.languages.${lang}`)
                : `${t(`screenText.languages.${lang}`)} (${t('settings.ai.screenText.noModels')})`
            };
          })}
        />
      </SettingRow>
    </Panel>
  );
};
