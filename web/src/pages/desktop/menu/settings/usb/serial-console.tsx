import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, message, Typography } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/virtual-device.ts';
import {
  fetchCommands,
  scriptArgs,
  serialConsoleScript as scriptName
} from '@/lib/serial-console-commands.ts';
import { Panel } from '@/components/ui/settings.tsx';

type SerialConsoleStatus = {
  serialNumber: string;
  vendorId?: string;
  productId?: string;
  port: string;
  script: { path: string; available: boolean; sha256?: string; size?: number };
  disk: { file: string; available: boolean; attached: boolean; stale: boolean };
};

type TestResult = { ok: boolean; state: string; response?: string };

const diskCommand = 'mount -o ro /dev/disk/by-label/NANOKVM /mnt && sh /mnt/setup.sh';

/*
 * The headless-Linux console helper: shown while USB serial is enabled.
 * Lets the administrator copy the command that runs the setup script on the
 * managed computer, see whether the helper disk is attached, and check from
 * here that a login answers on the serial port.
 */
export const SerialConsole = ({ serial, disk }: { serial: boolean; disk: boolean }) => {
  const { t } = useTranslation();
  const [status, setStatus] = useState<SerialConsoleStatus>();
  const [usbAddress, setUsbAddress] = useState<string>();
  const [testing, setTesting] = useState(false);
  const [result, setResult] = useState<TestResult>();

  const load = useCallback(async () => {
    try {
      const rsp = await api.getSerialConsole();
      if (rsp.code === 0) setStatus(rsp.data as SerialConsoleStatus);
    } catch {
      /* The row stays without details; the composition page reports load errors. */
    }
    try {
      const rsp = await api.getUsbInternet();
      const address = rsp.code === 0 ? rsp.data.address : undefined;
      setUsbAddress(address ? address.split('/')[0] : undefined);
    } catch {
      setUsbAddress(undefined);
    }
  }, []);

  useEffect(() => {
    if (!serial) return;
    void load();
    // S03usbdev attaches or removes the helper disk with the composition.
    window.addEventListener(api.usbCompositionChangedEvent, load);
    return () => window.removeEventListener(api.usbCompositionChangedEvent, load);
  }, [serial, load]);

  async function test() {
    setTesting(true);
    setResult(undefined);
    try {
      const rsp = await api.testSerialConsole();
      if (rsp.code === 0) setResult(rsp.data as TestResult);
      else message.error(t('settings.usb.serialConsole.testFailed'));
    } catch {
      message.error(t('settings.usb.serialConsole.testFailed'));
    } finally {
      setTesting(false);
    }
  }

  if (!serial) return null;
  const serialArg = scriptArgs(status);
  const sha256 = status?.script.available ? status.script.sha256 : undefined;
  const commands = sha256
    ? fetchCommands(
        { https: window.location.protocol === 'https:', browser: window.location.origin },
        usbAddress,
        sha256,
        serialArg
      )
    : undefined;
  const command = (label: string, text: string) => (
    <div className="space-y-1">
      <div className="text-fg-muted text-xs">{label}</div>
      <Typography.Text code copyable className="block text-xs break-all">
        {text}
      </Typography.Text>
    </div>
  );
  return (
    <Panel className="space-y-3 text-sm">
      <div>
        <div className="font-medium">{t('settings.usb.serialConsole.title')}</div>
        <p className="text-fg-muted m-0 text-xs">{t('settings.usb.serialConsole.description')}</p>
      </div>
      {commands && (
        <>
          {command(t('settings.usb.serialConsole.fromNetwork'), commands.network)}
          {commands.usb && command(t('settings.usb.serialConsole.fromUsbNetwork'), commands.usb)}
          {command(t('settings.usb.serialConsole.withWget'), commands.wget)}
          <div className="text-fg-muted font-mono text-[10px] break-all">sha256 {sha256}</div>
        </>
      )}
      {status?.disk.attached ? (
        <>
          {command(t('settings.usb.serialConsole.diskAttached'), diskCommand + serialArg)}
          {status.disk.stale && (
            <div className="text-warning text-xs">{t('settings.usb.serialConsole.diskStale')}</div>
          )}
        </>
      ) : (
        <div className="text-fg-muted text-xs">
          {disk
            ? t('settings.usb.serialConsole.diskPending')
            : t('settings.usb.serialConsole.diskNeedsDisk')}
        </div>
      )}
      <p className="text-fg-muted m-0 text-xs">{t('settings.usb.serialConsole.applyHint')}</p>
      <div className="flex items-center gap-3">
        <Button size="small" loading={testing} onClick={() => void test()}>
          {t('settings.usb.serialConsole.test')}
        </Button>
        <a
          className="text-xs"
          href={`/${scriptName}`}
          download={scriptName}
          target="_blank"
          rel="noreferrer"
        >
          {t('settings.usb.serialConsole.download')}
        </a>
      </div>
      {result && (
        <Alert
          type={result.ok ? 'success' : 'warning'}
          showIcon
          message={t(`settings.usb.serialConsole.states.${result.state}`, {
            defaultValue: t('settings.usb.serialConsole.states.error')
          })}
          description={
            result.response ? <code className="text-xs">{result.response}</code> : undefined
          }
        />
      )}
    </Panel>
  );
};
