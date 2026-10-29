import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Collapse, message, Radio, Switch } from 'antd';
import axios from 'axios';
import { ChevronDownIcon, FileUpIcon, RefreshCwIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as acmeApi from '@/api/extensions/acme.ts';
import * as api from '@/api/vm.ts';
import {
  certificateNames,
  certificateState,
  certificateTone,
  reloadAvailable,
  TlsStatus
} from '@/lib/tls-certificate.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { Panel, SettingRow, StatusBadge } from '@/components/ui/settings.tsx';

import { Acme } from './acme.tsx';

function restartInterruptedRequest(err: unknown): boolean {
  if (!axios.isAxiosError(err)) return false;
  const status = err.response?.status;
  return status === undefined || (status >= 502 && status <= 504);
}

export const Tls = () => {
  const { t } = useTranslation();

  const [status, setStatus] = useState<TlsStatus>();
  const [isEnabled, setIsEnabled] = useState(false);
  const [isLoading, setIsLoading] = useState(false);

  const load = useCallback(async () => {
    try {
      const rsp = await api.getTLS();
      if (rsp.code === 0) {
        setStatus(rsp.data as TlsStatus);
        setIsEnabled((rsp.data as TlsStatus).enabled);
      }
    } catch {
      /* the switch keeps following the page protocol */
    }
  }, []);

  useEffect(() => {
    setIsEnabled(window.location.protocol === 'https:');
    void load();
  }, [load]);

  async function update() {
    if (isLoading) return;
    setIsLoading(true);

    const enable = !isEnabled;
    const confirmed = await confirmAction({
      title: t(
        enable ? 'settings.network.tls.confirmEnable' : 'settings.network.tls.confirmDisable'
      ),
      content: t('settings.network.tls.confirmRestart'),
      danger: true
    });
    if (!confirmed) {
      // The switch shows loading while the dialog is open; clear it on cancel.
      setIsLoading(false);
      return;
    }

    try {
      const rsp = await api.setTLS(enable);
      if (rsp.code !== 0) {
        message.error(rsp.msg || t('settings.network.tls.failed'));
        setIsLoading(false);
        return;
      }
    } catch (err) {
      // The server restarts itself to apply the change and may drop the
      // connection before its reply arrives (or a proxy may answer 502-504
      // meanwhile). Only a definite HTTP refusal means it was not applied.
      if (!restartInterruptedRequest(err)) {
        console.log(err);
        message.error(t('settings.network.tls.failed'));
        setIsLoading(false);
        return;
      }
    }
    setIsEnabled(enable);

    // The server restarts after accepting the change; reload once it is back.
    const seconds = enable ? 30 : 10;
    setTimeout(() => {
      reload(enable);
    }, seconds * 1000);
  }

  function reload(enable: boolean) {
    if (!enable) {
      // Plain HTTP listens on its own port; do not reuse the HTTPS one.
      const target = new URL(window.location.href);
      target.protocol = 'http:';
      target.port = '';
      window.open(target.toString(), '_blank');
    }

    window.location.reload();
  }

  return (
    <>
      <SettingRow
        label="HTTPS"
        description={t('settings.network.tls.description')}
        help={t('settings.network.tls.tip')}
        htmlFor="network-tls"
      >
        <Switch
          id="network-tls"
          aria-label="HTTPS"
          aria-describedby="network-tls-description"
          checked={isEnabled}
          loading={isLoading}
          onChange={update}
        />
      </SettingRow>
      <Certificate status={status} onChange={setStatus} reload={load} />
    </>
  );
};

type CertificateInfo = NonNullable<TlsStatus['certificate']>;

const CertificateFields = ({
  info,
  date
}: {
  info: CertificateInfo;
  date: (iso: string) => string;
}) => {
  const { t } = useTranslation();
  return (
    <dl className="text-fg-muted m-0 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
      <dt className="m-0">{t('settings.network.tls.certificate.subject')}</dt>
      <dd className="text-fg m-0 break-all">{info.subject}</dd>
      <dt className="m-0">{t('settings.network.tls.certificate.issuer')}</dt>
      <dd className="m-0 break-all">
        {info.issuer}
        {info.selfSigned && ` · ${t('settings.network.tls.certificate.selfSigned')}`}
      </dd>
      {certificateNames(info).length > 0 && (
        <>
          <dt className="m-0">{t('settings.network.tls.certificate.names')}</dt>
          <dd className="m-0 break-all">{certificateNames(info).join(', ')}</dd>
        </>
      )}
      <dt className="m-0">{t('settings.network.tls.certificate.validity')}</dt>
      <dd className={info.expired ? 'text-danger m-0' : 'm-0'}>
        {date(info.notBefore)} – {date(info.notAfter)} · {info.keyType}
      </dd>
      <dt className="m-0">SHA-256</dt>
      <dd className="m-0 font-mono break-all">{info.fingerprint}</dd>
    </dl>
  );
};

type Source = TlsStatus['source'];

/*
 * The certificate HTTPS presents and where it comes from: the generated
 * self-signed pair, an uploaded one or one issued by acme.sh. Only the
 * chosen source's controls show. Changing the pair applies to new
 * connections at once; the server does not restart and the video keeps
 * running. While HTTPS is off the block starts folded.
 */
const Certificate = ({
  status,
  onChange,
  reload: refresh
}: {
  status?: TlsStatus;
  onChange: (status: TlsStatus) => void;
  reload: () => Promise<void>;
}) => {
  const { t, i18n } = useTranslation();
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [source, setSource] = useState<Source>();
  const [open, setOpen] = useState(false);
  const opened = useRef(false);

  const info = status?.certificate;
  const state = status ? certificateState(status) : undefined;
  const current: Source | undefined = status
    ? status.source === 'acme'
      ? 'acme'
      : status.custom
        ? 'custom'
        : 'generated'
    : undefined;
  const date = (iso: string) =>
    new Date(iso).toLocaleDateString(i18n.language, { dateStyle: 'medium' });

  // The source in use is the one shown, also after an upload or an issue.
  useEffect(() => {
    if (current) setSource(current);
  }, [current]);

  // Unfolded once when HTTPS is on; after that it stays as the user leaves it.
  useEffect(() => {
    if (!status || opened.current) return;
    opened.current = true;
    setOpen(status.enabled);
  }, [status]);

  async function choose() {
    const ok = await confirmAction({
      title: t('settings.network.tls.certificate.upload'),
      content: (
        <>
          <p className="mt-0">{t('settings.network.tls.certificate.uploadHint')}</p>
          <p className="mb-0">{t('settings.network.tls.certificate.uploadConfirm')}</p>
        </>
      ),
      okText: t('settings.network.tls.certificate.upload')
    });
    if (ok) input.current?.click();
  }

  async function upload(files: FileList | null) {
    if (!files?.length || busy) return;
    // One combined PEM or the certificate and key as separate files: the
    // server picks the certificate blocks and the key block out of the text.
    const texts = await Promise.all(Array.from(files).map((file) => file.text()));
    setBusy(true);
    try {
      const data = new FormData();
      data.append(
        'cert',
        new Blob([texts.join('\n')], { type: 'application/x-pem-file' }),
        'cert.pem'
      );
      const rsp = await api.uploadTLSCertificate(data);
      if (rsp.code === 0) {
        onChange(rsp.data as TlsStatus);
        message.success(t('settings.network.tls.certificate.uploaded'));
      } else {
        message.error(rsp.msg || t('settings.network.tls.certificate.failed'));
      }
    } catch {
      message.error(t('settings.network.tls.certificate.failed'));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    const ok = await confirmAction({
      title: t('settings.network.tls.certificate.removeTitle'),
      content: t('settings.network.tls.certificate.removeConfirm'),
      okText: t('settings.network.tls.certificate.remove'),
      danger: true
    });
    if (!ok) return;
    setBusy(true);
    try {
      const rsp = await api.deleteTLSCertificate();
      if (rsp.code === 0) {
        onChange(rsp.data as TlsStatus);
        message.success(t('settings.network.tls.certificate.removed'));
      } else {
        message.error(rsp.msg || t('settings.network.tls.certificate.failed'));
      }
    } catch {
      message.error(t('settings.network.tls.certificate.failed'));
    } finally {
      setBusy(false);
    }
  }

  // Back to the generated pair from an ACME one: the same as removing it
  // on the ACME side.
  async function removeAcme() {
    const ok = await confirmAction({
      title: t('settings.network.acme.removeTitle'),
      content: t('settings.network.acme.removeConfirm'),
      okText: t('settings.network.acme.remove'),
      danger: true
    });
    if (!ok) return;
    setBusy(true);
    try {
      const rsp = await acmeApi.remove();
      if (rsp.code === 0) message.success(t('settings.network.acme.removed'));
      else message.error(rsp.msg || t('settings.network.acme.failed'));
      await refresh();
    } catch {
      message.error(t('settings.network.acme.failed'));
    } finally {
      setBusy(false);
    }
  }

  async function reload() {
    setBusy(true);
    try {
      const rsp = await api.reloadTLS();
      if (rsp.code === 0) {
        onChange(rsp.data as TlsStatus);
        message.success(t('settings.network.tls.certificate.reloaded'));
      } else {
        message.error(rsp.msg || t('settings.network.tls.certificate.failed'));
      }
    } catch {
      message.error(t('settings.network.tls.certificate.failed'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel flush className="overflow-hidden">
      <details
        className="group"
        open={open}
        onToggle={(e) => setOpen((e.currentTarget as HTMLDetailsElement).open)}
      >
        <summary className="flex cursor-pointer list-none items-center justify-between gap-3 px-4 py-3 text-sm [&::-webkit-details-marker]:hidden">
          <span className="font-medium">{t('settings.network.tls.certificate.title')}</span>
          <span className="flex min-w-0 items-center gap-2">
            {state && (
              <StatusBadge tone={certificateTone[state]}>
                {state === 'error' ? status?.error : t(`settings.network.tls.certificate.${state}`)}
              </StatusBadge>
            )}
            <ChevronDownIcon
              size={16}
              className="text-fg-muted shrink-0 transition-transform group-open:rotate-180"
            />
          </span>
        </summary>
        <div className="space-y-3 px-4 pb-4">
          <p className="text-fg-muted m-0 text-xs">
            {t('settings.network.tls.certificate.description')}
          </p>
          {status && !status.enabled && (
            <p className="text-fg-muted m-0 text-xs">
              {t('settings.network.tls.certificate.offNote')}
            </p>
          )}
          {info && (
            // The badge says what kind of certificate is in use; its fields
            // are for checking it, so they stay folded until asked for.
            <Collapse
              ghost
              size="small"
              className="-mx-2"
              items={[
                {
                  key: 'details',
                  label: (
                    <span className="text-fg-muted text-xs">
                      {t('settings.network.tls.certificate.details')}
                    </span>
                  ),
                  children: <CertificateFields info={info} date={date} />
                }
              ]}
            />
          )}
          {status && reloadAvailable(status) && (
            <Button icon={<RefreshCwIcon size={16} />} disabled={busy} onClick={reload}>
              {t('settings.network.tls.certificate.reload')}
            </Button>
          )}
          <Radio.Group
            aria-label={t('settings.network.tls.certificate.title')}
            className="flex flex-col gap-1"
            value={source}
            disabled={!status}
            onChange={(e) => setSource(e.target.value as Source)}
            options={[
              { value: 'generated', label: t('settings.network.tls.certificate.generated') },
              { value: 'custom', label: t('settings.network.tls.certificate.custom') },
              { value: 'acme', label: t('settings.network.acme.title') }
            ]}
          />
          {source === 'generated' && current && current !== 'generated' && (
            <Button danger disabled={busy} onClick={current === 'acme' ? removeAcme : remove}>
              {t(
                current === 'acme'
                  ? 'settings.network.acme.remove'
                  : 'settings.network.tls.certificate.remove'
              )}
            </Button>
          )}
          <input
            ref={input}
            type="file"
            multiple
            hidden
            accept=".pem,.crt,.cer,.key,.txt"
            aria-label={t('settings.network.tls.certificate.upload')}
            onChange={(e) => {
              void upload(e.target.files);
              e.target.value = '';
            }}
          />
          {source === 'custom' && (
            <div className="flex flex-wrap items-center gap-3">
              <Button
                icon={<FileUpIcon size={16} />}
                loading={busy}
                disabled={!status}
                onClick={() => void choose()}
              >
                {t('settings.network.tls.certificate.upload')}
              </Button>
              {current === 'custom' && (
                <Button danger disabled={busy} onClick={remove}>
                  {t('settings.network.tls.certificate.remove')}
                </Button>
              )}
            </div>
          )}
          {/* Kept mounted so a running issue keeps being followed. */}
          <div className={source === 'acme' ? undefined : 'hidden'}>
            <Acme onChange={() => void refresh()} />
          </div>
        </div>
      </details>
    </Panel>
  );
};
