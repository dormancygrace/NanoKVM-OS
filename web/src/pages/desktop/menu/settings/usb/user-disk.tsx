import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, message, Select, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as storage from '@/api/storage.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { Panel, SettingRow, StatusBadge } from '@/components/ui/settings.tsx';

type UserDiskStatus = {
  file: string;
  label: string;
  sizes: number[];
  exists: boolean;
  sizeMiB?: number;
  modifiedAt?: string;
  enabled: boolean;
  attached: boolean;
};

const sizeLabel = (mib: number) => (mib >= 1024 ? `${mib / 1024} GiB` : `${mib} MiB`);

/*
 * The user disk: a writable FAT32 image on the data partition that the
 * computer sees as a third drive of the virtual storage (BIOS update files,
 * profiles). Create it here, then turn it on; turning it on or off is a
 * composition change, so the USB devices reconnect.
 */
export const UserDisk = ({
  disk,
  enabled,
  busy,
  onToggle
}: {
  disk: boolean;
  enabled: boolean;
  busy: boolean;
  onToggle: (enabled: boolean) => Promise<void>;
}) => {
  const { t } = useTranslation();
  const [status, setStatus] = useState<UserDiskStatus>();
  const [size, setSize] = useState(256);
  const [working, setWorking] = useState(false);

  const load = useCallback(async () => {
    try {
      const rsp = await storage.getUserDisk();
      if (rsp.code === 0) setStatus(rsp.data as UserDiskStatus);
    } catch {
      /* the card stays without details */
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load, enabled]);

  async function create() {
    if (!status) return;
    if (status.exists) {
      const ok = await confirmAction({
        title: t('settings.usb.userDisk.recreateTitle'),
        content: t('settings.usb.userDisk.recreateConfirm'),
        okText: t('settings.usb.userDisk.recreate'),
        danger: true
      });
      if (!ok) return;
    }
    setWorking(true);
    try {
      const rsp = await storage.createUserDisk(size);
      if (rsp.code === 0) {
        setStatus(rsp.data as UserDiskStatus);
        message.success(t('settings.usb.userDisk.created'));
      } else {
        message.error(
          rsp.code === -2
            ? t('settings.usb.userDisk.turnOffFirst')
            : t('settings.usb.userDisk.failed')
        );
      }
    } catch {
      message.error(t('settings.usb.userDisk.failed'));
    } finally {
      setWorking(false);
    }
  }

  async function remove() {
    const ok = await confirmAction({
      title: t('settings.usb.userDisk.deleteTitle'),
      content: t('settings.usb.userDisk.deleteConfirm'),
      okText: t('settings.usb.userDisk.delete'),
      danger: true
    });
    if (!ok) return;
    setWorking(true);
    try {
      const rsp = await storage.deleteUserDisk();
      if (rsp.code === 0) setStatus(rsp.data as UserDiskStatus);
      else
        message.error(
          rsp.code === -2
            ? t('settings.usb.userDisk.turnOffFirst')
            : t('settings.usb.userDisk.failed')
        );
    } catch {
      message.error(t('settings.usb.userDisk.failed'));
    } finally {
      setWorking(false);
    }
  }

  async function toggle(next: boolean) {
    // Both ways reconnect USB; off is the one that can cut a write short.
    const ok = await confirmAction({
      title: t(next ? 'settings.usb.userDisk.enableTitle' : 'settings.usb.userDisk.disableTitle'),
      content: t(next ? 'settings.usb.userDisk.reconnect' : 'settings.usb.userDisk.ejectFirst'),
      danger: !next
    });
    if (!ok) return;
    await onToggle(next);
    await load();
  }

  const state = !status
    ? undefined
    : !status.exists
      ? 'none'
      : status.attached
        ? 'attached'
        : status.enabled
          ? 'pending'
          : 'off';

  return (
    <Panel className="space-y-4">
      <SettingRow
        label={t('settings.usb.userDisk.title')}
        description={t('settings.usb.userDisk.description')}
        htmlFor="usb-user-disk"
      >
        <Switch
          id="usb-user-disk"
          checked={enabled}
          disabled={busy || working || !disk || !status?.exists}
          loading={busy}
          onChange={(next) => void toggle(next)}
        />
      </SettingRow>
      {!disk && <Alert type="info" showIcon message={t('settings.usb.userDisk.needsDisk')} />}
      {state && (
        <div className="flex flex-wrap items-center gap-3 text-sm">
          <StatusBadge
            tone={state === 'attached' ? 'success' : state === 'pending' ? 'warning' : 'neutral'}
          >
            {t(`settings.usb.userDisk.states.${state}`)}
          </StatusBadge>
          {status?.exists && (
            <span className="text-fg-muted">
              {sizeLabel(status.sizeMiB ?? 0)} · FAT32 · {status.label}
            </span>
          )}
        </div>
      )}
      <div className="flex flex-wrap items-center gap-3">
        <Select
          className="w-32"
          value={size}
          disabled={working || enabled}
          onChange={(value) => setSize(value)}
          options={(status?.sizes ?? [64, 128, 256, 512, 1024]).map((mib) => ({
            value: mib,
            label: sizeLabel(mib)
          }))}
        />
        <Button loading={working} disabled={enabled || !status} onClick={create}>
          {t(status?.exists ? 'settings.usb.userDisk.recreate' : 'settings.usb.userDisk.create')}
        </Button>
        {status?.exists && (
          <Button danger disabled={working || enabled} onClick={remove}>
            {t('settings.usb.userDisk.delete')}
          </Button>
        )}
      </div>
      <p className="text-fg-muted m-0 text-xs">{t('settings.usb.userDisk.notice')}</p>
    </Panel>
  );
};
