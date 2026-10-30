import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { Alert, Button, message, Progress, Spin } from 'antd';
import { useAtom, useAtomValue } from 'jotai';
import { DownloadIcon, ExternalLinkIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { checkAddonCatalog, getAddonCatalog, type AddonCatalog } from '@/api/addons.ts';
import { getRuntimeStatus, installRuntime, uninstallRuntime } from '@/api/picoclaw.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { addonInventoryAtom } from '@/jotai/addons';
import { picoclawRuntimeStatusAtom } from '@/jotai/picoclaw.ts';
import { rustDeskStatusAtom } from '@/jotai/rustdesk.ts';
import { Robot } from '@/components/icons/robot.tsx';
import { confirmAction } from '@/components/ui/confirm.ts';

import { AcmeAddon } from './acme';
import { AddonCard } from './addon-card';
import { CatalogAddonCard, CatalogStatus } from './catalog-addon';
import { IpmiAddon } from './ipmi';
import { RustDeskAddon } from './rustdesk';

export const Addons = ({
  onOpen,
  onOpenRustDesk,
  onOpenAcme,
  onOpenBmc
}: {
  onOpen: () => void;
  onOpenRustDesk: () => void;
  onOpenAcme: () => void;
  onOpenBmc: () => void;
}) => {
  const { t } = useTranslation();
  const [status, setStatus] = useAtom(picoclawRuntimeStatusAtom);
  const [busy, setBusy] = useState(false);
  const [loadError, setLoadError] = useState('');
  const pending = useRef(false);
  const generation = useRef(0);
  const inventory = useAtomValue(addonInventoryAtom);
  const rustDesk = useAtomValue(rustDeskStatusAtom);
  const [acmeInstalled, setAcmeInstalled] = useState<boolean>();
  // The add-ons of the NanoKVM repository, from the daily update check: a new
  // one appears here without an OS release.
  const [catalog, setCatalog] = useState<AddonCatalog>();
  const [checking, setChecking] = useState(false);
  const loadCatalog = useCallback(async () => {
    try {
      const response = await getAddonCatalog();
      if (response.code === 0) setCatalog(response.data);
    } catch {
      // The cards below work without the list; the next poll retries.
    }
  }, []);
  useEffect(() => {
    void loadCatalog();
    return pollWhileVisible(() => void loadCatalog(), 5000);
  }, [loadCatalog]);
  const checkCatalog = async () => {
    setChecking(true);
    try {
      const response = await checkAddonCatalog();
      if (response.code !== 0) throw new Error(response.msg);
      await loadCatalog();
    } catch (err) {
      void message.error(err instanceof Error ? err.message : t('settings.software.addons.failed'));
    } finally {
      setChecking(false);
    }
  };
  // Add-ons with a page of their own keep their card.
  const ipmiEntry = catalog?.addons.find((addon) => addon.name === 'nanokvm-ipmi');
  const others =
    catalog?.addons.filter((addon) => !['nanokvm-rustdesk', 'nanokvm-ipmi'].includes(addon.name)) ??
    [];
  const refresh = useCallback(async () => {
    if (pending.current) return;
    pending.current = true;
    const started = generation.current;
    try {
      const response = await getRuntimeStatus();
      if (started !== generation.current) return;
      if (response.code !== 0) throw new Error(response.msg);
      setStatus(response.data);
      setLoadError('');
    } catch (err) {
      if (started === generation.current)
        setLoadError(err instanceof Error ? err.message : t('settings.software.requestFailed'));
    } finally {
      pending.current = false;
    }
  }, [setStatus, t]);
  useEffect(() => {
    void refresh();
    const stop = pollWhileVisible(() => void refresh(), 2000);
    const invalidate = () => {
      generation.current++;
    };
    return () => {
      invalidate();
      stop();
    };
  }, [refresh]);

  const run = async (remove: boolean) => {
    if (busy || status?.installing) return;
    setBusy(true);
    generation.current++;
    try {
      const response = await (remove ? uninstallRuntime() : installRuntime());
      if (response.code !== 0) throw new Error(response.msg);
      if (response.data?.status) setStatus(response.data.status);
      await refresh();
    } catch (err) {
      void message.error(err instanceof Error ? err.message : t('settings.software.requestFailed'));
    } finally {
      setBusy(false);
    }
  };

  const picoclaw = (
    <AddonCard key="picoclaw" title="PicoClaw" icon={<Robot size={24} />}>
      <p className="text-fg m-0 text-sm">{t('settings.software.addons.picoclawDescription')}</p>
      <a
        className="text-info inline-flex items-center gap-1 text-sm no-underline!"
        href="https://github.com/sipeed/picoclaw/releases/latest"
        target="_blank"
        rel="noreferrer"
      >
        {t('settings.software.addons.source')} <ExternalLinkIcon size={14} />
      </a>
      {(loadError || status?.last_error) && (
        <Alert type="error" title={loadError || status?.last_error} showIcon />
      )}
      {!status ? (
        <Spin size="small" />
      ) : status.installing ? (
        <div>
          <div className="text-fg text-sm">
            {t(`picoclaw.install.stages.${status.install_stage || 'preparing'}`)}
          </div>
          <Progress percent={status.install_progress || 0} status="active" />
        </div>
      ) : status.installed ? (
        <div className="flex flex-wrap gap-2">
          <Button type="primary" disabled={busy} onClick={onOpen}>
            {t('settings.software.addons.open')}
          </Button>
          <Button
            type="text"
            loading={busy}
            icon={<Trash2Icon size={16} />}
            onClick={async () => {
              const confirmed = await confirmAction({
                title: t('picoclaw.uninstall.confirmTitle'),
                content: t('picoclaw.uninstall.confirmContent'),
                okText: t('picoclaw.uninstall.confirmOk'),
                cancelText: t('picoclaw.uninstall.confirmCancel'),
                danger: true,
                safeDefault: true
              });
              if (confirmed) void run(true);
            }}
          >
            {t('picoclaw.uninstall.menuLabel')}
          </Button>
        </div>
      ) : (
        <Button
          type="primary"
          loading={busy}
          icon={<DownloadIcon size={16} />}
          onClick={() => void run(false)}
        >
          {t('settings.software.addons.install')}
        </Button>
      )}
    </AddonCard>
  );
  // Each card once, in Installed or Available. A card whose state is not
  // known yet waits under Available.
  const cards: { installed?: boolean; card: ReactNode }[] = [
    { installed: status?.installed, card: picoclaw },
    {
      installed: rustDesk?.installed ?? inventory?.rustdesk?.installed,
      card: <RustDeskAddon key="rustdesk" onOpen={onOpenRustDesk} />
    },
    {
      installed: acmeInstalled,
      card: <AcmeAddon key="acme" onOpen={onOpenAcme} onInstalled={setAcmeInstalled} />
    },
    ...(ipmiEntry
      ? [
          {
            installed: inventory?.ipmi?.installed,
            card: <IpmiAddon key="ipmi" onOpen={onOpenBmc} entry={ipmiEntry} os={catalog?.os} />
          }
        ]
      : []),
    ...others.map((addon) => ({
      installed: !!addon.installed,
      card: (
        <CatalogAddonCard
          key={addon.name}
          addon={addon}
          os={catalog?.os}
          listed={!!catalog?.checkedAt}
          onChanged={(next) => (next ? setCatalog(next) : void loadCatalog())}
        />
      )
    }))
  ];
  const installed = cards.filter((item) => item.installed).map((item) => item.card);
  const available = cards.filter((item) => !item.installed).map((item) => item.card);

  return (
    <div className="space-y-6">
      <CatalogStatus catalog={catalog} checking={checking} onCheck={() => void checkCatalog()} />
      {installed.length > 0 && (
        <section className="space-y-4">
          <h3 className="text-fg-muted m-0 text-sm font-medium">
            {t('settings.software.addons.installedSection')}
          </h3>
          {installed}
        </section>
      )}
      {available.length > 0 && (
        <section className="space-y-4">
          <h3 className="text-fg-muted m-0 text-sm font-medium">
            {t('settings.software.addons.availableSection')}
          </h3>
          {available}
        </section>
      )}
    </div>
  );
};
