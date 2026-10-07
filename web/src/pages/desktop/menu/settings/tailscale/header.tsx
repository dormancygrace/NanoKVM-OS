import { useState } from 'react';
import { Popconfirm, Popover } from 'antd';
import { CircleStopIcon, EllipsisIcon, LoaderIcon, RotateCwIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/tailscale.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { IconButton } from '@/components/ui/settings.tsx';

import { VPNVersion } from '../vpn/version';
import type { State } from './types.ts';
import { Uninstall } from './uninstall.tsx';

type HeaderProps = {
  state: State | undefined;
  onSuccess: () => void;
};

type Loading = '' | 'restarting' | 'stopping';

export const Header = ({ state, onSuccess }: HeaderProps) => {
  const { t } = useTranslation();

  const [loading, setLoading] = useState<Loading>('');

  function restart() {
    if (loading !== '') return;
    setLoading('restarting');

    api
      .restart()
      .then((rsp) => {
        if (rsp.code !== 0) showRequestError(rsp);
      })
      .catch((err) => showRequestError(err))
      .finally(() => {
        setLoading('');
        onSuccess();
      });
  }

  function stop() {
    if (loading !== '') return;
    setLoading('stopping');

    api
      .stop()
      .then((rsp) => {
        if (rsp.code !== 0) showRequestError(rsp);
      })
      .catch((err) => showRequestError(err))
      .finally(() => {
        setLoading('');
        onSuccess();
      });
  }

  return (
    <div className="flex items-center justify-between">
      <VPNVersion name="tailscale" />

      <div className="flex items-center space-x-2">
        {state && ['notLogin', 'stopped', 'running'].includes(state) && (
          <>
            {/* restart button */}
            <Popconfirm
              title={t('settings.tailscale.restart')}
              onConfirm={restart}
              okText={t('settings.tailscale.okBtn')}
              cancelText={t('settings.tailscale.cancelBtn')}
              placement="bottom"
              disabled={loading !== ''}
            >
              <IconButton
                label={t('settings.tailscale.restartAction')}
                className="text-green-500 hover:text-green-500/80"
                icon={
                  loading === 'restarting' ? (
                    <LoaderIcon className="animate-spin" size={18} />
                  ) : (
                    <RotateCwIcon size={18} />
                  )
                }
              />
            </Popconfirm>

            {/* stop button */}
            <Popconfirm
              title={t('settings.tailscale.stop')}
              description={t('settings.tailscale.stopDesc')}
              onConfirm={stop}
              okText={t('settings.tailscale.okBtn')}
              cancelText={t('settings.tailscale.cancelBtn')}
              placement="bottom"
              disabled={loading !== ''}
            >
              <IconButton
                label={t('settings.tailscale.stopAction')}
                className="text-red-500 hover:text-red-500/80"
                icon={
                  loading === 'stopping' ? (
                    <LoaderIcon className="animate-spin" size={18} />
                  ) : (
                    <CircleStopIcon size={18} />
                  )
                }
              />
            </Popconfirm>
          </>
        )}

        {/* more button */}
        {state && state !== 'notInstall' && (
          <Popover
            content={
              <div className="flex min-w-[250px] flex-col">
                <Uninstall onSuccess={onSuccess} />
              </div>
            }
            placement="bottom"
            trigger="click"
          >
            <IconButton
              label={t('settings.tailscale.moreActions')}
              className="text-white"
              icon={<EllipsisIcon size={18} />}
            />
          </Popover>
        )}
      </div>
    </div>
  );
};
