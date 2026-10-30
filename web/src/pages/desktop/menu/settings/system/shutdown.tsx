import { useState } from 'react';
import { Button, Checkbox } from 'antd';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';
import { showRequestError } from '@/lib/show-request-error';
import { confirmAction } from '@/components/ui/confirm';
import { SettingsSection } from '@/components/ui/settings';

// Draft copy uses i18next fallbacks; existing Weblate translations are untouched.
export const Shutdown = () => {
  const { t } = useTranslation();
  const [magicWake, setMagicWake] = useState(false);
  const [busy, setBusy] = useState(false);
  const title = t('settings.shutdown.title', { defaultValue: 'Shut down NanoKVM' });
  const explanation = t('settings.shutdown.description', {
    defaultValue:
      'Stops video, keyboard, virtual disks, SSH and add-ons. Wait for confirmation before disconnecting NanoKVM power. If power stays connected, use the restart page to start NanoKVM OS again. The connected computer stays on.'
  });

  async function shutdown() {
    if (busy) return;
    if (
      !(await confirmAction({
        title,
        content: explanation,
        okText: title,
        cancelText: t('settings.device.cancelBtn'),
        danger: true,
        safeDefault: true
      }))
    )
      return;
    setBusy(true);
    let token: string | undefined;
    let commitRejected = false;
    try {
      const prepared = await http.post<{ token: string }>(
        '/api/vm/system/shutdown',
        { action: 'prepare', magicWake },
        { sensitive: true }
      );
      if (prepared.code !== 0) throw new Error(prepared.msg);
      token = prepared.data.token;
      // Persist the recovery capability before authorizing any service stop.
      sessionStorage.setItem('nanokvm-standby-key', token);
      if (sessionStorage.getItem('nanokvm-standby-key') !== token)
        throw new Error('The browser could not save the restart key.');
      const committed = await http.post(
        '/api/vm/system/shutdown',
        { action: 'commit', token },
        { sensitive: true }
      );
      if (committed.code !== 0) {
        commitRejected = true;
        throw new Error(committed.msg);
      }
      window.location.assign('/standby');
    } catch (error) {
      if (token && !commitRejected) {
        // The commit reply may have been lost as services stopped. Preserve
        // the key and open the status page; never infer success from a timeout.
        try {
          const cancelled = await http.post(
            '/api/vm/system/shutdown',
            { action: 'cancel', token },
            { sensitive: true }
          );
          if (cancelled.code !== 0) {
            window.location.assign('/standby');
            return;
          }
        } catch {
          window.location.assign('/standby');
          return;
        }
      }
      showRequestError(error);
      setBusy(false);
    }
  }

  return (
    <SettingsSection title={title} description={explanation}>
      <Checkbox
        checked={magicWake}
        disabled={busy}
        onChange={(e) => setMagicWake(e.target.checked)}
      >
        {t('settings.shutdown.magic', {
          defaultValue:
            'Also allow a Wake-on-LAN magic packet. Anyone who can reach the listener can restart NanoKVM without a password.'
        })}
      </Checkbox>
      <div>
        <Button danger loading={busy} onClick={() => void shutdown()}>
          {title}
        </Button>
      </div>
    </SettingsSection>
  );
};
