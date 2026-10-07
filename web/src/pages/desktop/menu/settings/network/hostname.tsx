import { useEffect, useState } from 'react';
import { Alert, Button, Input } from 'antd';
import { CheckIcon, ClipboardPenIcon, XIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { requestErrorText } from '@/lib/request-error.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { IconButton, SettingRow } from '@/components/ui/settings.tsx';

export const Hostname = ({ editable = false }: { editable?: boolean }) => {
  const { t } = useTranslation();

  const [isLoading, setIsLoading] = useState(false);
  const [hostname, setHostname] = useState('');
  // The failed load request; its text is derived when rendering.
  const [loadError, setLoadError] = useState<{ error: unknown } | null>(null);

  const [editState, setEditState] = useState<'' | 'editing' | 'edited'>('');
  const [input, setInput] = useState('');

  useEffect(() => {
    getHostname();
  }, []);

  function getHostname() {
    setIsLoading(true);

    api
      .getHostname()
      .then((rsp) => {
        if (rsp.data?.hostname) {
          setHostname(rsp.data?.hostname);
        }
      })
      .catch((err) => setLoadError({ error: err }))
      .finally(() => {
        setIsLoading(false);
      });
  }

  function showInput() {
    setInput(hostname);
    setEditState('editing');
  }

  function update() {
    if (input === hostname) {
      setEditState('');
      return;
    }

    if (isLoading) return;
    setIsLoading(true);

    api
      .setHostname(input)
      .then((rsp) => {
        if (rsp.code !== 0) {
          showRequestError(rsp);
          return;
        }

        setHostname(input);
        setEditState('edited');
      })
      .catch((err) => showRequestError(err))
      .finally(() => {
        setIsLoading(false);
      });
  }

  const loadErrorText = loadError && requestErrorText(loadError.error, t('error.requestFailed'));

  return (
    <>
      {loadErrorText && <Alert type="error" showIcon message={loadErrorText} />}
      <SettingRow
        label={t('settings.about.hostname')}
        description={
          editState === 'edited' ? (
            <span className="text-success">{t('settings.about.hostnameUpdated')}</span>
          ) : undefined
        }
      >
        {editState === 'editing' ? (
          <div className="flex items-center gap-1">
            <Input
              aria-label={t('settings.about.hostname')}
              disabled={isLoading}
              style={{ width: 180 }}
              value={input}
              onChange={(e) => setInput(e.target.value)}
            />
            <Button
              size="small"
              aria-label={t('settings.about.hostnameSave')}
              icon={<CheckIcon size={14} />}
              onClick={update}
            />
            <Button
              size="small"
              aria-label={t('settings.about.hostnameCancel')}
              icon={<XIcon size={14} />}
              onClick={() => setEditState('')}
            />
          </div>
        ) : (
          <div className="flex items-center gap-2">
            <span>{hostname}</span>
            {editable && (
              <IconButton
                label={t('settings.about.hostnameEdit')}
                className="text-fg-muted hover:text-info!"
                icon={<ClipboardPenIcon size={14} />}
                onClick={showInput}
              />
            )}
          </div>
        )}
      </SettingRow>
    </>
  );
};
