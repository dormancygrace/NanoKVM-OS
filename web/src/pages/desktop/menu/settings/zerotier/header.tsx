import { useState } from 'react';
import { Alert, Popconfirm, Popover } from 'antd';
import { CircleStopIcon, EllipsisIcon, LoaderCircleIcon, RotateCwIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/zerotier.ts';
import { IconButton } from '@/components/ui/settings.tsx';

import { Uninstall } from './uninstall.tsx';

type HeaderProps = {
  state: api.ZerotierState | undefined;
  onSuccess: () => void;
};

type Loading = '' | 'restarting' | 'stopping';

export const Header = ({ state, onSuccess }: HeaderProps) => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState<Loading>('');
  const [errMsg, setErrMsg] = useState('');
  const installed = !!state && state !== 'notInstall';

  function run(action: Exclude<Loading, ''>) {
    if (loading) return;
    setLoading(action);
    setErrMsg('');
    (action === 'restarting' ? api.restart() : api.stop())
      .then((rsp) => {
        if (rsp.code !== 0) setErrMsg(rsp.msg);
      })
      .catch((err) =>
        setErrMsg(
          err?.message ||
            t(
              action === 'restarting'
                ? 'settings.zerotier.error.restartFailed'
                : 'settings.zerotier.error.stopFailed'
            )
        )
      )
      .finally(() => {
        setLoading('');
        onSuccess();
      });
  }

  const spinner = <LoaderCircleIcon className="animate-spin" size={16} />;

  return (
    <>
      {installed && (
        <div className="flex items-center justify-end gap-2">
          <Popconfirm
            title={t('settings.zerotier.restart')}
            onConfirm={() => run('restarting')}
            okText={t('settings.zerotier.okBtn')}
            cancelText={t('settings.zerotier.cancelBtn')}
            placement="bottom"
            disabled={!!loading}
          >
            <IconButton
              label={t('settings.zerotier.restartAction')}
              className="text-success hover:text-success/80"
              icon={loading === 'restarting' ? spinner : <RotateCwIcon size={16} />}
            />
          </Popconfirm>
          <Popconfirm
            title={t('settings.zerotier.stop')}
            description={t('settings.zerotier.stopDesc')}
            onConfirm={() => run('stopping')}
            okText={t('settings.zerotier.okBtn')}
            cancelText={t('settings.zerotier.cancelBtn')}
            placement="bottom"
            disabled={!!loading}
          >
            <IconButton
              label={t('settings.zerotier.stopAction')}
              className="text-danger hover:text-danger/80"
              icon={loading === 'stopping' ? spinner : <CircleStopIcon size={16} />}
            />
          </Popconfirm>
          <Popover
            content={<Uninstall onSuccess={onSuccess} />}
            placement="bottomRight"
            arrow={false}
            trigger="click"
          >
            <IconButton
              label={t('settings.zerotier.moreActions')}
              className="text-fg"
              icon={<EllipsisIcon size={16} />}
            />
          </Popover>
        </div>
      )}
      {errMsg && <Alert type="error" showIcon message={errMsg} />}
    </>
  );
};
