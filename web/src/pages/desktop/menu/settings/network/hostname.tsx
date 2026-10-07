import { useEffect, useState } from 'react';
import { Button, Input } from 'antd';
import { CheckIcon, ClipboardPenIcon, XIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { showRequestError } from '@/lib/show-request-error.ts';

export const Hostname = ({ editable = false }: { editable?: boolean }) => {
  const { t } = useTranslation();

  const [isLoading, setIsLoading] = useState(false);
  const [hostname, setHostname] = useState('');

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
      .catch((err) => showRequestError(err))
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

  return (
    <div className="space-y-1">
      <div className="flex w-full items-center justify-between">
        <span>{t('settings.about.hostname')}</span>

        {editState === 'editing' ? (
          <div className="flex items-center space-x-1">
            <Input
              disabled={isLoading}
              style={{ width: 150 }}
              value={input}
              onChange={(e) => setInput(e.target.value)}
            />
            <Button size="small" icon={<CheckIcon size={14} />} onClick={update} />
            <Button size="small" icon={<XIcon size={14} />} onClick={() => setEditState('')} />
          </div>
        ) : (
          <div className="flex items-center space-x-2">
            <span>{hostname}</span>
            {editable && (
              <Button
                size="small"
                type="text"
                aria-label={t('settings.about.hostname')}
                title={t('settings.about.hostname')}
                className="text-neutral-400 hover:!text-blue-500"
                icon={<ClipboardPenIcon size={14} />}
                onClick={showInput}
              />
            )}
          </div>
        )}
      </div>

      {editState === 'edited' && (
        <div className="flex w-full justify-end text-xs text-green-500">
          {t('settings.about.hostnameUpdated')}
        </div>
      )}
    </div>
  );
};
