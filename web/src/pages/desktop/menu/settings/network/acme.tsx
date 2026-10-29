import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Button, Collapse, Input, InputNumber, message, Radio, Select, Switch } from 'antd';
import { DownloadIcon, RefreshCwIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/acme.ts';
import {
  acmeAuthorities,
  AcmeProvider,
  AcmeStatus,
  hasWildcard,
  jobState,
  needsEab,
  needsEmail,
  parseDomains,
  providerFields,
  providerMatches
} from '@/lib/acme.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { SettingRow, StatusBadge } from '@/components/ui/settings.tsx';

const POLL_MS = 2000;

/*
 * A certificate from a public CA through acme.sh: install acme.sh from its
 * latest GitHub release, issue for the device's domain names with the HTTP
 * or a DNS challenge, renew from the server. Issue and renew run in the
 * background; the panel polls while they run and tells the certificate
 * panel when they are done.
 */
export const Acme = ({ onChange }: { onChange: () => void }) => {
  const { t, i18n } = useTranslation();
  const [status, setStatus] = useState<AcmeStatus>();
  const [providers, setProviders] = useState<AcmeProvider[]>([]);
  const [busy, setBusy] = useState(false);
  const [checking, setChecking] = useState(false);

  // The form.
  const [domains, setDomains] = useState('');
  const [email, setEmail] = useState('');
  const [ca, setCa] = useState('letsencrypt');
  const [challenge, setChallenge] = useState<'http' | 'dns'>('http');
  const [hook, setHook] = useState<string>();
  const [alt, setAlt] = useState(false);
  const [env, setEnv] = useState<Record<string, string>>({});
  const [dnsSleep, setDnsSleep] = useState<number | null>(null);
  const [eabKid, setEabKid] = useState('');
  const [eabHmac, setEabHmac] = useState('');
  const prefilled = useRef(false);
  const checked = useRef(false);

  const load = useCallback(async () => {
    try {
      const rsp = await api.getStatus();
      if (rsp.code !== 0) return undefined;
      const next = rsp.data as AcmeStatus;
      setStatus(next);
      if (next.installed && !next.latest && !checked.current) {
        // The last check does not survive a server restart; ask once per
        // visit so the update button shows without a click.
        checked.current = true;
        api
          .checkUpdate()
          .then((check) => {
            if (check.code === 0) setStatus(check.data as AcmeStatus);
          })
          .catch(() => {
            /* GitHub not reachable: the check button stays */
          });
      }
      if (!prefilled.current && next.settings) {
        prefilled.current = true;
        setDomains(next.settings.domains.join(', '));
        setEmail(next.settings.email ?? '');
        setCa(next.settings.ca);
        setChallenge(next.settings.challenge);
        setHook(next.settings.dnsProvider);
        setDnsSleep(next.settings.dnsSleep ?? null);
      }
      return next;
    } catch {
      return undefined;
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // The DNS hooks, once acme.sh is there and the DNS challenge is chosen.
  useEffect(() => {
    if (!status?.installed || challenge !== 'dns' || providers.length) return;
    api
      .getProviders()
      .then((rsp) => {
        if (rsp.code === 0) setProviders(rsp.data as AcmeProvider[]);
      })
      .catch(() => {
        /* the select stays empty */
      });
  }, [status?.installed, challenge, providers.length]);

  // Poll while a job runs; refresh the certificate panel when it ends.
  useEffect(() => {
    if (!status?.running) return;
    const timer = setInterval(async () => {
      const next = await load();
      if (next && !next.running) onChange();
    }, POLL_MS);
    return () => clearInterval(timer);
  }, [status?.running, load, onChange]);

  const provider = useMemo(() => providers.find((p) => p.hook === hook), [providers, hook]);
  const fields = providerFields(provider, alt);
  const domainList = parseDomains(domains);
  const date = (iso: string) =>
    new Date(iso).toLocaleDateString(i18n.language, { dateStyle: 'medium' });

  async function run(
    action: () => Promise<{ code: number; msg: string; data: unknown }>,
    okKey?: string
  ) {
    setBusy(true);
    try {
      const rsp = await action();
      if (rsp.code === 0) {
        setStatus(rsp.data as AcmeStatus);
        if (okKey) message.success(t(okKey));
        onChange();
      } else {
        message.error(rsp.msg || t('settings.network.acme.failed'));
      }
    } catch {
      message.error(t('settings.network.acme.failed'));
    } finally {
      setBusy(false);
    }
  }

  async function install(update: boolean) {
    const ok = await confirmAction({
      title: t(
        update ? 'settings.network.acme.updateTitle' : 'settings.network.acme.installTitle',
        {
          version: status?.latest?.tag ?? ''
        }
      ),
      content: t('settings.network.acme.installConfirm')
    });
    if (!ok) return;
    await run(api.install, 'settings.network.acme.installed');
  }

  async function check() {
    setChecking(true);
    try {
      const rsp = await api.checkUpdate();
      if (rsp.code === 0) {
        const next = rsp.data as AcmeStatus;
        setStatus(next);
        message.info(
          t(
            next.updateAvailable
              ? 'settings.network.acme.updateFound'
              : 'settings.network.acme.upToDate',
            { version: next.latest?.tag ?? '' }
          )
        );
      } else {
        message.error(rsp.msg || t('settings.network.acme.failed'));
      }
    } catch {
      message.error(t('settings.network.acme.failed'));
    } finally {
      setChecking(false);
    }
  }

  async function issue() {
    const ok = await confirmAction({
      title: t('settings.network.acme.issueTitle'),
      content: t(
        challenge === 'http'
          ? 'settings.network.acme.issueConfirmHttp'
          : 'settings.network.acme.issueConfirmDns'
      )
    });
    if (!ok) return;
    const dnsEnv: Record<string, string> = {};
    for (const field of fields) {
      if (env[field.name]) dnsEnv[field.name] = env[field.name];
    }
    await run(() =>
      api.issue({
        domains: domainList,
        email,
        ca,
        challenge,
        dnsProvider: challenge === 'dns' ? hook : undefined,
        dnsSleep: challenge === 'dns' && dnsSleep ? dnsSleep : undefined,
        dnsEnv,
        eabKid: needsEab(ca) ? eabKid : undefined,
        eabHmacKey: needsEab(ca) ? eabHmac : undefined
      })
    );
  }

  async function remove() {
    const ok = await confirmAction({
      title: t('settings.network.acme.removeTitle'),
      content: t('settings.network.acme.removeConfirm'),
      okText: t('settings.network.acme.remove'),
      danger: true
    });
    if (!ok) return;
    await run(api.remove, 'settings.network.acme.removed');
  }

  async function uninstall() {
    const ok = await confirmAction({
      title: t('settings.network.acme.uninstallTitle'),
      content: t('settings.network.acme.uninstallConfirm'),
      okText: t('settings.network.acme.uninstall'),
      danger: true
    });
    if (!ok) return;
    await run(api.uninstall, 'settings.network.acme.uninstalled');
  }

  const state = status ? jobState(status) : undefined;
  const canIssue =
    !!status?.installed &&
    !status.running &&
    domainList.length > 0 &&
    (challenge === 'http' ? !hasWildcard(domainList) : !!hook) &&
    (!needsEmail(ca) || email.trim() !== '') &&
    (!needsEab(ca) || (eabKid.trim() !== '' && eabHmac.trim() !== ''));

  return (
    <div className="space-y-4">
      <SettingRow
        label={t('settings.network.acme.title')}
        description={t('settings.network.acme.description')}
      >
        {status &&
          (status.installed ? (
            <span className="text-fg-muted text-xs">acme.sh {status.version}</span>
          ) : (
            <StatusBadge tone="neutral">{t('settings.network.acme.notInstalled')}</StatusBadge>
          ))}
      </SettingRow>

      {status && !status.installed && (
        <div className="flex flex-wrap items-center gap-3">
          <Button
            type="primary"
            icon={<DownloadIcon size={16} />}
            loading={busy}
            onClick={() => install(false)}
          >
            {t('settings.network.acme.install')}
          </Button>
          <span className="text-fg-muted text-xs">{t('settings.network.acme.installHint')}</span>
        </div>
      )}

      {status?.installed && (
        <>
          <div className="flex flex-wrap items-center gap-3">
            {status.updateAvailable && status.latest && (
              <Button
                type="primary"
                icon={<DownloadIcon size={16} />}
                loading={busy}
                disabled={status.running}
                onClick={() => install(true)}
              >
                {t('settings.network.acme.update', { version: status.latest.tag })}
              </Button>
            )}
            <Button
              icon={<RefreshCwIcon size={16} />}
              loading={checking}
              disabled={busy}
              onClick={check}
            >
              {t('settings.network.acme.check')}
            </Button>
            {status.latest && !status.updateAvailable && (
              <span className="text-fg-muted text-xs">
                {t('settings.network.acme.latest', { version: status.latest.tag })}
              </span>
            )}
          </div>

          {status.certificate && (
            <div className="border-line space-y-2 rounded-md border p-3 text-sm">
              <div className="flex flex-wrap items-center gap-3">
                <StatusBadge tone={status.inUse ? 'success' : 'neutral'}>
                  {t(status.inUse ? 'settings.network.acme.inUse' : 'settings.network.acme.issued')}
                </StatusBadge>
                <span className="text-fg-muted text-xs">
                  {status.certificate.subject} · {t('settings.network.acme.validUntil')}{' '}
                  {date(status.certificate.notAfter)}
                  {status.nextRenewal &&
                    ` · ${t('settings.network.acme.renewsAfter')} ${date(status.nextRenewal)}`}
                </span>
              </div>
              <div className="flex flex-wrap items-center gap-3">
                <Button disabled={busy || status.running} onClick={() => run(api.renew)}>
                  {t('settings.network.acme.renew')}
                </Button>
                <Button danger disabled={busy || status.running} onClick={remove}>
                  {t('settings.network.acme.remove')}
                </Button>
              </div>
            </div>
          )}

          {state && status.lastRun && (
            <Collapse
              size="small"
              items={[
                {
                  key: 'run',
                  label: (
                    <StatusBadge
                      tone={
                        state === 'running'
                          ? 'warning'
                          : state === 'failed'
                            ? 'danger'
                            : state === 'ok'
                              ? 'success'
                              : 'neutral'
                      }
                    >
                      {t(`settings.network.acme.run.${status.lastRun.action}`)}:{' '}
                      {t(`settings.network.acme.run.${state}`)}
                      {status.lastRun.error && ` · ${status.lastRun.error}`}
                    </StatusBadge>
                  ),
                  children: (
                    <pre className="m-0 max-h-64 overflow-auto text-xs whitespace-pre-wrap">
                      {status.lastRun.output || t('settings.network.acme.noOutput')}
                    </pre>
                  )
                }
              ]}
            />
          )}

          <Collapse
            size="small"
            defaultActiveKey={status.certificate ? [] : ['issue']}
            items={[
              {
                key: 'issue',
                label: t(
                  status.certificate
                    ? 'settings.network.acme.reissue'
                    : 'settings.network.acme.issue'
                ),
                children: (
                  <div className="space-y-4">
                    <SettingRow
                      label={t('settings.network.acme.domains')}
                      description={t('settings.network.acme.domainsHint')}
                      htmlFor="acme-domains"
                      stacked
                    >
                      <Input
                        id="acme-domains"
                        value={domains}
                        placeholder="kvm.example.org, www.kvm.example.org"
                        onChange={(e) => setDomains(e.target.value)}
                      />
                    </SettingRow>
                    <SettingRow
                      label={t('settings.network.acme.email')}
                      description={t('settings.network.acme.emailHint')}
                      htmlFor="acme-email"
                      stacked
                    >
                      <Input
                        id="acme-email"
                        type="email"
                        value={email}
                        onChange={(e) => setEmail(e.target.value)}
                      />
                    </SettingRow>
                    <SettingRow label={t('settings.network.acme.ca')} htmlFor="acme-ca">
                      <Select
                        id="acme-ca"
                        className="w-64"
                        value={ca}
                        options={acmeAuthorities}
                        onChange={(value) => setCa(value)}
                      />
                    </SettingRow>
                    {needsEab(ca) && (
                      <div className="grid gap-3 sm:grid-cols-2">
                        <Input
                          aria-label="EAB key ID"
                          placeholder="EAB key ID"
                          value={eabKid}
                          onChange={(e) => setEabKid(e.target.value)}
                        />
                        <Input.Password
                          aria-label="EAB HMAC key"
                          placeholder="EAB HMAC key"
                          value={eabHmac}
                          autoComplete="off"
                          onChange={(e) => setEabHmac(e.target.value)}
                        />
                      </div>
                    )}
                    <SettingRow
                      label={t('settings.network.acme.challenge')}
                      description={t(
                        challenge === 'http'
                          ? 'settings.network.acme.challengeHttpHint'
                          : 'settings.network.acme.challengeDnsHint'
                      )}
                      stacked
                    >
                      <Radio.Group
                        value={challenge}
                        onChange={(e) => setChallenge(e.target.value)}
                        options={[
                          { value: 'http', label: t('settings.network.acme.challengeHttp') },
                          { value: 'dns', label: t('settings.network.acme.challengeDns') }
                        ]}
                      />
                    </SettingRow>
                    {challenge === 'http' && hasWildcard(domainList) && (
                      <Alert
                        type="warning"
                        showIcon
                        message={t('settings.network.acme.wildcardNeedsDns')}
                      />
                    )}
                    {challenge === 'dns' && (
                      <>
                        <SettingRow
                          label={t('settings.network.acme.provider')}
                          htmlFor="acme-provider"
                          stacked
                        >
                          <Select
                            id="acme-provider"
                            className="w-full"
                            showSearch
                            value={hook}
                            placeholder={t('settings.network.acme.providerPlaceholder')}
                            options={providers.map((p) => ({ value: p.hook, label: p.name }))}
                            filterOption={(input, option) => {
                              const p = providers.find((x) => x.hook === option?.value);
                              return !!p && providerMatches(p, input);
                            }}
                            onChange={(value) => {
                              setHook(value);
                              setAlt(false);
                              setEnv({});
                            }}
                          />
                        </SettingRow>
                        {provider && (
                          <div className="space-y-3">
                            {provider.docs && (
                              <a
                                className="text-xs"
                                href={provider.docs}
                                target="_blank"
                                rel="noreferrer"
                              >
                                {t('settings.network.acme.providerDocs', { name: provider.name })}
                              </a>
                            )}
                            {!!provider.optionsAlt?.length && (
                              <SettingRow
                                label={t('settings.network.acme.altCredentials')}
                                htmlFor="acme-alt"
                              >
                                <Switch id="acme-alt" checked={alt} onChange={setAlt} />
                              </SettingRow>
                            )}
                            {fields.map((field) => (
                              <SettingRow
                                key={field.name}
                                label={field.name}
                                description={field.description}
                                htmlFor={`acme-env-${field.name}`}
                                stacked
                              >
                                <Input.Password
                                  id={`acme-env-${field.name}`}
                                  value={env[field.name] ?? ''}
                                  autoComplete="off"
                                  onChange={(e) => setEnv({ ...env, [field.name]: e.target.value })}
                                />
                              </SettingRow>
                            ))}
                            <SettingRow
                              label={t('settings.network.acme.dnsSleep')}
                              description={t('settings.network.acme.dnsSleepHint')}
                              htmlFor="acme-dns-sleep"
                            >
                              <InputNumber
                                id="acme-dns-sleep"
                                min={0}
                                max={3600}
                                value={dnsSleep}
                                onChange={(value) => setDnsSleep(value)}
                              />
                            </SettingRow>
                          </div>
                        )}
                      </>
                    )}
                    <div className="flex flex-wrap items-center gap-3">
                      <Button type="primary" loading={busy} disabled={!canIssue} onClick={issue}>
                        {t('settings.network.acme.issueButton')}
                      </Button>
                      <span className="text-fg-muted text-xs">
                        {t('settings.network.acme.issueHint')}
                      </span>
                    </div>
                  </div>
                )
              }
            ]}
          />

          <div>
            <Button danger size="small" disabled={busy || status.running} onClick={uninstall}>
              {t('settings.network.acme.uninstall')}
            </Button>
          </div>
        </>
      )}
    </div>
  );
};
