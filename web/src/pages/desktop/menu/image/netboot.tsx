import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/storage.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';

type Props = { isOpen: boolean; onEnabled: (value: boolean) => void };

// Network boot: the official iPXE image on the virtual CD drive loads the
// netboot.xyz menus of one release over the NanoKVM's internet connection.
export const Netboot = ({ isOpen, onEnabled }: Props) => {
  const { t } = useTranslation();
  const [status, setStatus] = useState<api.NetbootStatus | null>(null);
  const [secureBoot, setSecureBoot] = useState(true);
  const [busy, setBusy] = useState<'' | 'update' | 'enable' | 'disable'>('');
  const [error, setError] = useState('');
  const callback = useRef(onEnabled);
  callback.current = onEnabled;
  const chosen = useRef(false);

  const refresh = useCallback(
    () =>
      api
        .getNetboot()
        .then((rsp) => {
          if (rsp.code !== 0) return;
          const data = rsp.data as api.NetbootStatus;
          setStatus(data);
          // The last choice is the default until the user picks again.
          if (!chosen.current) setSecureBoot(data.secureBoot || !data.ipxe);
          callback.current(data.enabled);
        })
        .catch(() => {}),
    []
  );

  useEffect(() => {
    refresh();
  }, [refresh]);
  useEffect(() => {
    if (!isOpen) return;
    refresh();
    return pollWhileVisible(refresh, 3000);
  }, [isOpen, refresh]);

  async function run(kind: 'update' | 'enable' | 'disable') {
    setBusy(kind);
    setError('');
    try {
      const rsp =
        kind === 'update'
          ? await api.updateNetboot()
          : kind === 'enable'
            ? await api.enableNetboot(secureBoot)
            : await api.disableNetboot();
      if (rsp.code !== 0) {
        setError(rsp.msg || t('image.netboot.failed'));
      } else if (rsp.data) {
        setStatus(rsp.data as api.NetbootStatus);
        callback.current(!!rsp.data.enabled);
      }
    } catch {
      setError(t('image.netboot.failed'));
    } finally {
      setBusy('');
      window.dispatchEvent(new Event('nanokvm:image-updated'));
      refresh();
    }
  }

  const ready = !!status?.usbNetwork && !!status?.virtualDisk;
  const uplink = status?.usbInternet?.state === 'active';

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-neutral-400">{t('image.netboot.hint')}</p>

      <div className="flex flex-col gap-1">
        <span>
          {status?.ipxe
            ? t('image.netboot.versions', { ipxe: status.ipxe, netbootxyz: status.netbootxyz })
            : t('image.netboot.notDownloaded')}
        </span>
        {status?.checkedAt && (
          <span className="text-sm text-neutral-400">
            {t('image.netboot.checked', { date: new Date(status.checkedAt).toLocaleString() })}
          </span>
        )}
        <div>
          <Button
            size="small"
            loading={busy === 'update'}
            disabled={!!busy}
            onClick={() => run('update')}
          >
            {t(busy === 'update' ? 'image.netboot.updating' : 'image.netboot.update')}
          </Button>
        </div>
      </div>

      <div className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-1">
          <span>{t('image.netboot.secureBoot')}</span>
          <span className="text-sm text-neutral-400">{t('image.netboot.secureBootHint')}</span>
        </div>
        <Switch
          checked={status?.enabled ? status.secureBoot : secureBoot}
          disabled={!!status?.enabled || !!busy}
          aria-label={t('image.netboot.secureBoot')}
          onChange={(value) => {
            chosen.current = true;
            setSecureBoot(value);
          }}
        />
      </div>

      {status && !ready && (
        <Alert type="warning" showIcon message={t('image.netboot.needComposition')} />
      )}
      {status?.browserMedia && (
        <Alert type="warning" showIcon message={t('image.netboot.browserMedia')} />
      )}
      {status?.enabled && (
        <div className="flex flex-col gap-1">
          <span>{t('image.netboot.on')}</span>
          <span className="text-sm text-neutral-400">
            {status.menuFetches > 0
              ? t('image.netboot.fetched', {
                  n: status.menuFetches,
                  time: new Date((status.lastFetch ?? 0) * 1000).toLocaleTimeString()
                })
              : t('image.netboot.waiting')}
          </span>
        </div>
      )}
      {status?.enabled && !uplink && (
        <Alert type="warning" showIcon message={t('image.netboot.noUplink')} />
      )}
      {error && <Alert type="error" showIcon message={error} />}

      {status?.enabled ? (
        <Button loading={busy === 'disable'} disabled={!!busy} onClick={() => run('disable')}>
          {t('image.netboot.disable')}
        </Button>
      ) : (
        <div className="flex flex-col gap-2">
          <span className="text-sm text-neutral-400">{t('image.netboot.enableNote')}</span>
          <Button
            type="primary"
            loading={busy === 'enable'}
            disabled={!!busy || !ready || !!status?.browserMedia}
            onClick={() => run('enable')}
          >
            {t(busy === 'enable' ? 'image.netboot.enabling' : 'image.netboot.enable')}
          </Button>
        </div>
      )}
    </div>
  );
};
