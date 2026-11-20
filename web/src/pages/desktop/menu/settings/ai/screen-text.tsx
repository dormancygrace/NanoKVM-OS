import { useEffect, useState } from 'react';
import { Alert, Button, message, Switch } from 'antd';
import { useAtom } from 'jotai';
import { DownloadIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  getScreenTextSettings,
  setScreenTextEnabled,
  setScreenTextLanguage,
  type ScreenTextSettings
} from '@/api/vm.ts';
import type { ApiResponse } from '@/lib/http.ts';
import { screenTextLanguages } from '@/lib/screen-text.ts';
import { screenTextChangeAtom, screenTextSettingsAtom } from '@/jotai/screen.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { Panel, SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

// Every answer carries all the settings as the server has them. One change
// runs at a time (the switch and the language buttons wait for each other,
// see screenTextChangeAtom), and only the answer to the latest request is
// applied: a read of the page still on its way when a change starts must not
// bring back the state from before that change. The count lives in the
// module, so it outlasts the page being left and opened again.
let latestRequest = 0;

// Settings > AI > Text recognition: reading the text on the screen with the
// TPU, on or off for the toolbar button, MCP and PicoClaw together, and the
// languages whose models are installed. The models are downloaded from the
// NanoKVM add-on repository on request. Each read picks its language (the
// reading dialog remembers the last one).

export const ScreenTextSettingsPage = () => {
  const { t } = useTranslation();
  const [settings, setSettings] = useAtom(screenTextSettingsAtom);
  const [changing, setChanging] = useAtom(screenTextChangeAtom);
  const [error, setError] = useState('');

  // Read when the page opens and again after each change, once no change is
  // on its way: a read during a change could bring the state from before it.
  // The read after a change also covers a failed request, after which the
  // page cannot tell what the server did: the answer to a download that
  // finished may have been lost on the way.
  useEffect(() => {
    if (changing) return;
    const request = ++latestRequest;
    getScreenTextSettings()
      .then((rsp) => {
        if (rsp.code === 0 && request === latestRequest) setSettings(rsp.data);
      })
      .catch(() => {});
  }, [changing, setSettings]);

  async function apply(send: () => Promise<ApiResponse<ScreenTextSettings>>) {
    const request = ++latestRequest;
    const rsp = await send();
    if (rsp.code !== 0) throw new Error(rsp.msg);
    if (request === latestRequest) setSettings(rsp.data);
  }

  async function toggle(enabled: boolean) {
    if (!settings || changing) return;
    setChanging('switch');
    try {
      await apply(() => setScreenTextEnabled(enabled));
    } catch {
      message.error(t('settings.ai.screenText.switchFailed'));
    } finally {
      setChanging('');
    }
  }

  async function change(language: string, installed: boolean) {
    if (changing) return;
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
    setChanging(language);
    setError('');
    try {
      await apply(() => setScreenTextLanguage(language, installed));
    } catch (err) {
      setError(err instanceof Error ? err.message : t('settings.ai.screenText.changeFailed'));
    } finally {
      setChanging('');
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
            loading={!settings || changing === 'switch'}
            disabled={!settings || !!changing}
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
                  loading={changing === language}
                  disabled={!settings || (!!changing && changing !== language)}
                  onClick={() => void change(language, false)}
                >
                  {t('settings.ai.screenText.remove')}
                </Button>
              ) : (
                <Button
                  icon={<DownloadIcon size={16} />}
                  loading={changing === language}
                  disabled={!settings || (!!changing && changing !== language)}
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
