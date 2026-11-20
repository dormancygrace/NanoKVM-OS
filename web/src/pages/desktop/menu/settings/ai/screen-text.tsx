import { useEffect, useState } from 'react';
import { Alert, Button, message, Switch } from 'antd';
import { useAtom } from 'jotai';
import { DownloadIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getScreenTextSettings, setScreenTextEnabled, setScreenTextLanguage } from '@/api/vm.ts';
import { screenTextLanguages } from '@/lib/screen-text.ts';
import { screenTextSettingsAtom } from '@/jotai/screen.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { Panel, SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

// Settings > AI > Text recognition: reading the text on the screen with the
// TPU, on or off for the toolbar button, MCP and PicoClaw together, and the
// languages whose models are installed. The models are downloaded from the
// NanoKVM add-on repository on request. Each read picks its language (the
// reading dialog remembers the last one).
export const ScreenTextSettingsPage = () => {
  const { t } = useTranslation();
  const [settings, setSettings] = useAtom(screenTextSettingsAtom);
  const [busy, setBusy] = useState(false);
  // The language being installed or removed.
  const [working, setWorking] = useState('');
  const [error, setError] = useState('');

  useEffect(() => {
    let active = true;
    getScreenTextSettings()
      .then((rsp) => {
        if (active && rsp.code === 0) setSettings(rsp.data);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [setSettings]);

  async function toggle(enabled: boolean) {
    if (!settings) return;
    setBusy(true);
    try {
      const rsp = await setScreenTextEnabled(enabled);
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setSettings({ ...settings, enabled });
    } catch {
      message.error(t('settings.ai.screenText.switchFailed'));
    } finally {
      setBusy(false);
    }
  }

  async function change(language: string, installed: boolean) {
    if (working) return;
    if (
      !installed &&
      !(await confirmAction({
        title: t('settings.ai.screenText.removeTitle'),
        content: t('settings.ai.screenText.removeConfirm', {
          language: t(`screenText.languages.${language}`)
        }),
        okText: t('settings.ai.screenText.remove'),
        danger: true,
        safeDefault: true
      }))
    )
      return;
    setWorking(language);
    setError('');
    try {
      const rsp = await setScreenTextLanguage(language, installed);
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setSettings(rsp.data);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('settings.ai.screenText.changeFailed'));
    } finally {
      setWorking('');
    }
  }

  const installed = (language: string) =>
    settings?.languages.some((lang) => lang.name === language && lang.installed) ?? false;

  return (
    <div className="space-y-6">
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
      <SettingsSection
        title={t('settings.ai.screenText.languagesTitle')}
        description={t('settings.ai.screenText.languagesDescription')}
      >
        {settings && !settings.installed && (
          <Alert type="info" showIcon title={t('settings.ai.screenText.notInstalled')} />
        )}
        {error && <Alert type="error" showIcon title={error} />}
        <Panel flush className="divide-line divide-y">
          {screenTextLanguages.map((language) => (
            <div key={language} className="flex items-center justify-between gap-3 px-4 py-3">
              <span className="text-fg min-w-0">{t(`screenText.languages.${language}`)}</span>
              {installed(language) ? (
                <Button
                  type="text"
                  icon={<Trash2Icon size={16} />}
                  loading={working === language}
                  disabled={!settings || (!!working && working !== language)}
                  onClick={() => void change(language, false)}
                >
                  {t('settings.ai.screenText.remove')}
                </Button>
              ) : (
                <Button
                  icon={<DownloadIcon size={16} />}
                  loading={working === language}
                  disabled={!settings || (!!working && working !== language)}
                  onClick={() => void change(language, true)}
                >
                  {t('settings.ai.screenText.install')}
                </Button>
              )}
            </div>
          ))}
        </Panel>
      </SettingsSection>
    </div>
  );
};
