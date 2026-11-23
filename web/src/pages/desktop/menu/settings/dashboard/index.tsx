import { Fragment, useEffect, useState, type ReactNode } from 'react';
import { useAuth } from '@/contexts/auth';
import { Alert, Button, Progress } from 'antd';
import { useAtomValue } from 'jotai';
import {
  ArrowUpRightIcon,
  CpuIcon,
  EthernetPortIcon,
  HardDriveIcon,
  MemoryStickIcon,
  TimerIcon,
  WifiIcon
} from 'lucide-react';
import { useTranslation } from 'react-i18next';

import type { LldpStatus, ServiceStatus, UpsStatus } from '@/api/extensions/netsvc.ts';
import type { MemoryStatus } from '@/api/vm';
import { formatDeviceTime, type DateTimeConfig } from '@/lib/date-time';
import { getEncoderCodec } from '@/lib/encoder';
import { http } from '@/lib/http';
import { formatVersion } from '@/lib/version';
import {
  captureReadyAtom,
  isHdmiEnabledAtom,
  videoModeAtom,
  videoSessionCountAtom
} from '@/jotai/screen';
import { HandshakeAge } from '@/components/handshake-age';
import { Netbird as NetbirdIcon } from '@/components/icons/netbird';
import { OpenVPNIcon } from '@/components/icons/openvpn';
import { Tailscale as TailscaleIcon } from '@/components/icons/tailscale';
import { WireGuardIcon } from '@/components/icons/wireguard';
import { Zerotier as ZerotierIcon } from '@/components/icons/zerotier';
import { Panel, StatusBadge } from '@/components/ui/settings.tsx';

import { summarizeVPN, vpnProviders } from '../vpn/providers.ts';

type CPU = { total: number; idle: number };
type Disk = {
  path: string;
  available: boolean;
  total: number;
  free: number;
  used: number;
  readOnly: boolean;
};
type Interface = {
  kind: string;
  name: string;
  mac: string;
  mtu: number;
  up: boolean;
  enabled?: boolean;
  connected: boolean;
  wireless: boolean;
  addresses: string[];
  received: number | null;
  sent: number | null;
};
type Snapshot = {
  system: {
    now: number;
    hostname: string;
    kernel: string;
    architecture: string;
    cores: number;
    uptime: number | null;
    cpu: CPU | null;
    cpuUsage: number | null;
    load: string[] | null;
    temperature: number | null;
    cpuFrequency: number | null;
    storage: Disk[];
    interfaces: Interface[];
  };
  memory: MemoryStatus | null;
  application: string;
};
type Video = {
  inputWidth: number;
  inputHeight: number;
  outputWidth: number;
  outputHeight: number;
  mjpegOutputWidth: number;
  mjpegOutputHeight: number;
  videoOutputWidth: number;
  videoOutputHeight: number;
  fps: number;
  measuredFps: number;
  effectiveFps: number;
  bitRate: number;
  quality: number;
};
type Profile = {
  id: string;
  name: string;
  state: string;
  address: string;
  enabled: boolean;
  lastHandshake?: number;
  mtu?: number;
  received?: number;
  sent?: number;
};
// A NanoKVM that announces itself on the local network (GET /api/network/peers).
type Peer = {
  name: string;
  host: string;
  addresses: string[];
  port: number;
  proto: string;
  model: string;
  self: boolean;
};
type Extras = {
  time?: { config: DateTimeConfig; synchronized: boolean; daemon: string };
  wifi?: { connected: boolean; ssid: string };
  wireguard?: { available: boolean; profiles: Profile[]; now?: number };
  openvpn?: { available: boolean; profiles: Profile[] };
  tailscale?: { state: string; ip: string; name: string };
  netbird?: { state: string; ip: string; name: string };
  zerotier?: { state: string; ip?: string };
  ups?: UpsStatus;
  lldp?: LldpStatus;
  snmp?: ServiceStatus;
  journal?: { entries: JournalEntry[] };
};
type JournalEntry = {
  seq: number;
  time: string;
  severity: 'info' | 'warning' | 'critical';
  title: string;
};
const journalTones = { info: 'neutral', warning: 'warning', critical: 'danger' } as const;
// how many of the newest events the panel shows
const recentEvents = 5;
// The services of Settings > Services that are installed on request.
const netServices = ['lldp', 'snmp', 'ups'] as const;
// What a UPS says about itself (ups.status of Network UPS Tools): the first
// of these flags it reports names its state.
const upsStates = [
  ['LB', 'lowBattery', 'danger'],
  ['OB', 'onBattery', 'warning'],
  ['OL', 'online', 'success']
] as const;
const bytes = (value?: number | null) => {
  if (value == null) return '—';
  const unit = value >= 1073741824 ? 'GiB' : 'MiB';
  return `${(value / (unit === 'GiB' ? 1073741824 : 1048576)).toFixed(1)} ${unit}`;
};
const percent = (used: number, total: number) =>
  total > 0 ? Math.max(0, Math.min(100, (used / total) * 100)) : 0;
async function read<T>(url: string): Promise<T> {
  const rsp = await http.request({ method: 'get', url, timeout: 8000 });
  if (rsp.code !== 0) throw new Error(rsp.msg);
  return rsp.data as T;
}

// caption: the line saying that the values are live. The overview, whose
// whole page they are, leaves it out.
export const Dashboard = ({
  navigate,
  caption = true
}: {
  navigate: (tab: string) => void;
  caption?: boolean;
}) => {
  const { t, i18n } = useTranslation();
  const { account } = useAuth();
  const admin = account.role === 'admin';
  const [data, setData] = useState<Snapshot>();
  const [video, setVideo] = useState<Video>();
  const [extra, setExtra] = useState<Extras>({});
  const [peers, setPeers] = useState<Peer[]>([]);
  const [cpu, setCpu] = useState<number | null>(null);
  const [errors, setErrors] = useState<string[]>([]);
  const [extraFailed, setExtraFailed] = useState(false);
  const enabled = useAtomValue(isHdmiEnabledAtom);
  const ready = useAtomValue(captureReadyAtom);
  const mode = useAtomValue(videoModeAtom);
  const sessions = useAtomValue(videoSessionCountAtom);
  const outputWidth = mode === 'mjpeg' ? video?.mjpegOutputWidth : video?.videoOutputWidth;
  const outputHeight = mode === 'mjpeg' ? video?.mjpegOutputHeight : video?.videoOutputHeight;

  useEffect(() => {
    let disposed = false;
    const timers: number[] = [];
    const report = (section: string, failed: boolean) => {
      if (disposed) return;
      setErrors((current) => {
        const others = current.filter((item) => item !== section);
        return failed ? [...others, section] : others;
      });
    };
    // Poll independently: a stalled video request must not delay CPU and memory.
    const pollSystem = async () => {
      if (!document.hidden) {
        try {
          const snapshot = await read<Snapshot>('/api/vm/dashboard');
          if (disposed) return;
          setData(snapshot);
          setCpu(snapshot.system.cpuUsage ?? null);
          report('system', false);
        } catch {
          if (disposed) return;
          setCpu(null);
          report('system', true);
        }
      }
      if (!disposed) timers[0] = window.setTimeout(() => void pollSystem(), 3000);
    };
    const pollVideo = async () => {
      if (!document.hidden) {
        try {
          const snapshot = await read<Video>('/api/vm/screen');
          if (disposed) return;
          setVideo(snapshot);
          report('video', false);
        } catch {
          report('video', true);
        }
      }
      if (!disposed) timers[1] = window.setTimeout(() => void pollVideo(), 3000);
    };
    void pollSystem();
    void pollVideo();
    return () => {
      disposed = true;
      timers.forEach((timer) => window.clearTimeout(timer));
    };
  }, []);
  useEffect(() => {
    let disposed = false;
    let timer: number | undefined;
    const poll = async () => {
      if (!document.hidden) {
        const paths: [keyof Extras, string][] = [['time', '/api/vm/date-time']];
        if (admin)
          paths.push(
            ['wifi', '/api/network/wifi'],
            ['openvpn', '/api/extensions/openvpn/status'],
            ['tailscale', '/api/extensions/tailscale/status'],
            ['netbird', '/api/extensions/netbird/status'],
            ['zerotier', '/api/extensions/zerotier/status'],
            ['wireguard', '/api/extensions/wireguard/status'],
            ['ups', '/api/extensions/ups/status?light=1'],
            ['lldp', '/api/extensions/lldp/status?light=1'],
            ['snmp', '/api/extensions/snmp/status?light=1'],
            [
              'journal',
              `/api/vm/journal?limit=${recentEvents}&lang=${encodeURIComponent(i18n.language)}`
            ]
          );
        const results = await Promise.allSettled(
          paths.map(async ([key, url]) => [key, await read(url)] as const)
        );
        if (disposed) return;
        setExtra((current) => {
          const next = { ...current };
          for (const result of results)
            if (result.status === 'fulfilled')
              Object.assign(next, { [result.value[0]]: result.value[1] });
          return next;
        });
        setExtraFailed(results.some((result) => result.status === 'rejected'));
      }
      if (!disposed) timer = window.setTimeout(() => void poll(), 30000);
    };
    void poll();
    return () => {
      disposed = true;
      window.clearTimeout(timer);
    };
  }, [admin, i18n.language]);

  // The other NanoKVMs on the network. Asking takes the device a second or
  // two of waiting for answers, so it has its own, slower turn.
  useEffect(() => {
    let disposed = false;
    let timer: number | undefined;
    const poll = async () => {
      if (!document.hidden) {
        try {
          const found = await read<{ peers: Peer[] }>('/api/network/peers');
          if (!disposed) setPeers(found.peers.filter((peer) => !peer.self));
        } catch {
          // the last list stays
        }
      }
      if (!disposed) timer = window.setTimeout(() => void poll(), 60000);
    };
    void poll();
    return () => {
      disposed = true;
      window.clearTimeout(timer);
    };
  }, []);

  const wireguardNames = new Map(
    extra.wireguard?.profiles.map((profile) => [profile.id, profile.name])
  );
  const sys = data?.system,
    memory = data?.memory;
  const disk = sys?.storage.find((item) => item.path === '/data' && item.available);
  const uptime =
    sys?.uptime == null
      ? '—'
      : t('dashboard.duration', {
          days: Math.floor(sys.uptime / 86400),
          hours: Math.floor((sys.uptime % 86400) / 3600),
          minutes: Math.floor((sys.uptime % 3600) / 60)
        });
  const size = (w?: number, h?: number) => (w && h ? `${w} × ${h}` : '—');
  // A panel is there for what is in use: a UPS that is read, the services
  // that are switched on, events once there are any.
  const upsValues =
    extra.ups?.running && Object.keys(extra.ups.values).length > 0 ? extra.ups.values : undefined;
  const upsFlags = upsValues?.['ups.status']?.split(' ') ?? [];
  const upsState = upsStates.find(([flag]) => upsFlags.includes(flag));
  const enabledServices = netServices.filter((name) => extra[name]?.enabled);
  // The VPNs that are installed; OpenVPN, installed with the system, once it
  // has a profile.
  const listedVPNs = vpnProviders.filter((provider) => {
    const data = extra[provider.id];
    if (!data || !summarizeVPN(data).installed) return false;
    return provider.id !== 'openvpn' || ('profiles' in data && data.profiles.length > 0);
  });
  const events = extra.journal?.entries.slice(0, recentEvents) ?? [];
  // The time of an event; with its date when it is a day or more old.
  const eventTime = (time: string) => {
    const at = new Date(time).getTime();
    const old = !!sys && sys.now - at >= 24 * 3600 * 1000;
    return formatDeviceTime(at, extra.time!.config, i18n.language, old);
  };
  const state = (key: string) => t(`dashboard.states.${key}`, { defaultValue: key });
  const line = (label: string, value: ReactNode) => (
    <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-x-4 gap-y-1 py-1 sm:flex sm:flex-wrap sm:justify-between">
      <dt className="text-fg-muted min-w-0 wrap-break-word">{label}</dt>
      <dd className="text-fg min-w-0 text-right wrap-break-word sm:ml-auto">{value ?? '—'}</dd>
    </div>
  );
  const section = (title: string, children: ReactNode, tab?: string) => (
    <Panel className="min-w-0">
      <div className="mb-3 flex items-center justify-between gap-2">
        <h3 className="m-0 text-sm font-medium">{title}</h3>
        {tab && (
          <Button
            type="text"
            size="small"
            aria-label={t('dashboard.open', { name: title })}
            onClick={() => navigate(tab)}
            icon={<ArrowUpRightIcon size={16} />}
          />
        )}
      </div>
      {children}
    </Panel>
  );
  const metric = (
    title: string,
    value: string,
    icon: ReactNode,
    detail?: string,
    fill?: number,
    tab?: string
  ) => (
    <Panel className="min-w-0">
      <div className="text-fg-muted mb-2 flex items-center gap-2 text-xs">
        {icon}
        <span className="min-w-0 flex-1">{title}</span>
        {tab && (
          // Negative margin keeps the header as tall as tiles without a link.
          <Button
            type="text"
            size="small"
            className="-my-1"
            aria-label={t('dashboard.open', { name: title })}
            onClick={() => navigate(tab)}
            icon={<ArrowUpRightIcon size={16} />}
          />
        )}
      </div>
      <div className="text-lg font-medium wrap-break-word tabular-nums">{value}</div>
      {detail && <div className="text-fg-muted mt-1 text-xs">{detail}</div>}
      {fill !== undefined && (
        <Progress
          percent={fill}
          showInfo={false}
          size="small"
          strokeColor="var(--color-info)"
          railColor="var(--color-neutral-700)"
        />
      )}
    </Panel>
  );
  const vpnProfiles = (kind: 'openvpn' | 'wireguard', name: string) => {
    const profiles = extra[kind]?.profiles;
    return (
      <div>
        <button
          type="button"
          className="text-fg hover:text-info inline-flex items-center gap-2 border-0! bg-transparent! !p-0 text-sm font-medium"
          onClick={() => navigate(`vpn-${kind}`)}
        >
          {kind === 'openvpn' ? <OpenVPNIcon /> : <WireGuardIcon />}
          {name}
        </button>
        {profiles?.length ? (
          profiles.map((profile) => {
            const iface = sys?.interfaces.find(
              (item) =>
                item.name === profile.id ||
                ((item.kind === 'tun' || item.kind === 'tap') &&
                  item.addresses.some((addr) => profile.address.split(/[,\s]+/).includes(addr)))
            );
            return (
              <div key={profile.id} className="mt-2 text-xs">
                <div className="flex justify-between gap-2">
                  <span className="break-all">{profile.name}</span>
                  <StatusBadge tone={profile.state === 'connected' ? 'success' : 'neutral'}>
                    {state(profile.state)}
                  </StatusBadge>
                </div>
                {profile.address && (
                  <div className="text-fg-muted mt-1 break-all">{profile.address}</div>
                )}
                {!!(profile.mtu || iface?.mtu) && (
                  <div className="text-fg-muted mt-1">MTU {profile.mtu || iface?.mtu}</div>
                )}
                {kind === 'wireguard' && profile.state !== 'off' && (
                  <div className="text-fg-muted mt-1">
                    RX {bytes(profile.received)} · TX {bytes(profile.sent)}
                  </div>
                )}
                {!!profile.lastHandshake && (
                  <div className="text-fg-muted mt-1">
                    {t('dashboard.handshake')}:{' '}
                    <HandshakeAge
                      timestamp={profile.lastHandshake!}
                      serverNow={extra.wireguard?.now}
                    />
                  </div>
                )}
              </div>
            );
          })
        ) : (
          <div className="text-fg-muted mt-1 text-xs">
            {profiles ? t('dashboard.noProfiles') : '—'}
          </div>
        )}
      </div>
    );
  };
  return (
    <div className="@container space-y-5 pb-6">
      {caption && (
        <div className="flex flex-col items-start gap-1 sm:flex-row sm:items-end sm:justify-between sm:gap-2">
          <span className="text-fg-muted max-w-full text-xs wrap-break-word">
            {t('dashboard.live')}
          </span>
        </div>
      )}
      {(errors.length > 0 || extraFailed) && (
        <Alert type="warning" showIcon message={t('dashboard.stale')} />
      )}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {metric(
          'CPU',
          cpu === null ? '—' : `${cpu.toFixed(0)}%`,
          <CpuIcon size={16} />,
          [
            sys?.cpuFrequency == null ? undefined : `${sys.cpuFrequency} MHz`,
            sys?.temperature == null ? undefined : `SoC ${sys.temperature.toFixed(1)} °C`
          ]
            .filter(Boolean)
            .join(' · ') || undefined,
          cpu ?? undefined,
          admin ? 'system-general' : undefined
        )}
        {/* What programs can still get, page cache included, like the free space next
            to it. The bars fill as the memory and the storage are used, like the
            one of the processor. */}
        {metric(
          t('dashboard.freeRam'),
          bytes(memory?.availableBytes),
          <MemoryStickIcon size={16} />,
          memory ? t('dashboard.of', { total: bytes(memory.totalBytes) }) : undefined,
          memory
            ? percent(memory.totalBytes - memory.availableBytes, memory.totalBytes)
            : undefined,
          admin ? 'system-memory' : undefined
        )}
        {metric(
          t('dashboard.freeStorage'),
          bytes(disk?.free),
          <HardDriveIcon size={16} />,
          disk ? t('dashboard.of', { total: bytes(disk.total) }) : undefined,
          disk ? percent(disk.total - disk.free, disk.total) : undefined
        )}
        {metric(t('dashboard.uptime'), uptime, <TimerIcon size={16} />)}
      </div>
      {/* One grid for every panel: when one is not shown the next takes its place. */}
      <div className="grid gap-4 md:grid-cols-2 @5xl:grid-cols-3">
        {section(
          t('dashboard.device'),
          <dl className="text-sm">
            {line(t('settings.about.hostname'), sys?.hostname)}
            {line(
              t('dashboard.application'),
              data?.application ? formatVersion(data.application) : undefined
            )}
            {line(t('dashboard.kernel'), sys?.kernel)}
            {line(t('dashboard.load'), sys?.load?.join(' / '))}
          </dl>,
          admin ? 'device' : undefined
        )}
        {/* A panel shows what is in use: with the capture switched off there
            is no video to describe. */}
        {(!ready || enabled) &&
          section(
            t('videoSettings.title'),
            <dl className="text-sm">
              {line(
                t('dashboard.capture'),
                t(!ready ? 'capture.checking' : 'videoSettings.captureOn')
              )}
              {line(
                t('videoSettings.input'),
                enabled ? size(video?.inputWidth, video?.inputHeight) : '—'
              )}
              {line(
                t('videoSettings.output'),
                enabled
                  ? size(outputWidth || video?.outputWidth, outputHeight || video?.outputHeight)
                  : '—'
              )}
              {line(
                t('screen.codec'),
                mode === 'mjpeg' ? 'MJPEG' : getEncoderCodec() === 'h265' ? 'H.265' : 'H.264'
              )}
              {line(
                t('screen.video'),
                mode === 'h264' ? 'WebRTC' : mode === 'direct' ? 'Direct' : 'MJPEG'
              )}
              {line(
                t('dashboard.fps'),
                video
                  ? t('videoSettings.fpsValue', {
                      value: `${enabled ? video.measuredFps : 0} / ${video.fps}`
                    })
                  : '—'
              )}
              {line(
                t(mode === 'mjpeg' ? 'screen.quality' : 'videoSettings.bitrate'),
                video
                  ? mode === 'mjpeg'
                    ? `${video.quality}%`
                    : t('videoSettings.mbps', { value: video.bitRate / 1000 })
                  : '—'
              )}
              {line(t('dashboard.sessions'), sessions)}
            </dl>,
            'video-general'
          )}
        {section(
          t('dashboard.memory'),
          <dl className="text-sm">
            {line(t('dashboard.available'), bytes(memory?.availableBytes))}
            {line(t('dashboard.cache'), bytes(memory?.cachedBytes))}
            {line(
              t('dashboard.zram'),
              memory
                ? memory.zram.enabled
                  ? t('dashboard.swapUsage', {
                      used: bytes(memory.zram.usedBytes),
                      size: memory.zram.sizeMiB
                    })
                  : state('off')
                : '—'
            )}
            {memory?.zram.enabled && line(t('dashboard.compression'), memory.zram.algorithm)}
            {line(
              t('dashboard.sdSwap'),
              memory
                ? memory.sd.enabled
                  ? t('dashboard.swapUsage', {
                      used: bytes(memory.sd.usedBytes),
                      size: memory.sd.sizeMiB
                    })
                  : state('off')
                : '—'
            )}
          </dl>,
          admin ? 'system-memory' : undefined
        )}
        {section(
          t('dashboard.storage'),
          <div className="space-y-4">
            {sys?.storage.map((item) => (
              <div key={item.path}>
                <div className="flex justify-between gap-2 text-sm">
                  <span>
                    {t(
                      item.path === '/'
                        ? 'dashboard.systemStorage'
                        : item.path === '/data'
                          ? 'dashboard.dataStorage'
                          : 'dashboard.bootStorage'
                    )}{' '}
                    <span className="text-fg-muted text-xs">{item.path}</span>
                  </span>
                  {item.readOnly && (
                    <StatusBadge tone="warning">{t('dashboard.readOnly')}</StatusBadge>
                  )}
                </div>
                {item.available ? (
                  <>
                    <Progress
                      size="small"
                      percent={percent(item.used, item.total)}
                      showInfo={false}
                      strokeColor="var(--color-info)"
                      railColor="var(--color-neutral-700)"
                    />
                    <div className="text-fg-muted text-xs">
                      {t('dashboard.diskSpace', {
                        free: bytes(item.free),
                        total: bytes(item.total)
                      })}
                    </div>
                  </>
                ) : (
                  <div className="text-fg-muted mt-1 text-xs">{t('dashboard.unavailable')}</div>
                )}
              </div>
            )) ?? '—'}
          </div>
        )}
        {sys?.interfaces
          .filter(
            (iface) =>
              iface.enabled !== false &&
              (!admin ||
                !(
                  iface.kind === 'wireguard' ||
                  iface.name === 'tailscale0' ||
                  iface.kind === 'tun'
                ))
          )
          .map((iface) => (
            <div key={iface.name} className="grid min-w-0">
              {section(
                iface.wireless
                  ? t('settings.network.wifi.title')
                  : iface.name === 'eth0'
                    ? t('settings.network.ethernet.name')
                    : iface.name,
                <div className="text-sm">
                  <div className="mb-2 flex items-center gap-2">
                    {iface.kind === 'wireguard' ? (
                      <WireGuardIcon />
                    ) : iface.wireless ? (
                      <WifiIcon size={16} />
                    ) : (
                      <EthernetPortIcon size={16} />
                    )}
                    <span className="min-w-0 wrap-break-word" title={iface.name}>
                      {iface.kind === 'wireguard'
                        ? wireguardNames.get(iface.name) || 'WireGuard'
                        : iface.name}
                    </span>
                    {/* Beside the name: in a half-width column a right-aligned
                      badge ends up far from its interface. */}
                    <span className="ml-1 shrink-0">
                      <StatusBadge tone={iface.up && iface.connected ? 'success' : 'neutral'}>
                        {t(
                          iface.up && iface.connected
                            ? 'dashboard.connected'
                            : 'dashboard.disconnected'
                        )}
                      </StatusBadge>
                    </span>
                  </div>
                  {iface.kind === 'wireguard' && (
                    <div className="text-fg-muted mb-1 text-xs">
                      {wireguardNames.get(iface.name) ? 'WireGuard' : iface.name}
                    </div>
                  )}
                  {iface.wireless && iface.connected && extra.wifi?.connected && (
                    <p className="text-fg mb-1 break-all">{extra.wifi.ssid}</p>
                  )}
                  <div className={iface.connected ? 'text-fg' : 'text-fg-muted'}>
                    {iface.addresses.map((addr) => (
                      <div key={addr} className="break-all">
                        {addr}
                      </div>
                    ))}
                  </div>
                  <div className="text-fg-muted mt-2 text-xs">
                    MTU {iface.mtu}
                    {iface.mac ? ` · ${iface.mac}` : ''}
                  </div>
                  <div className="text-fg-muted mt-1 text-xs">
                    RX {bytes(iface.received)} · TX {bytes(iface.sent)}
                  </div>
                </div>,
                admin
                  ? iface.wireless
                    ? 'network-wifi'
                    : iface.name === 'eth0'
                      ? 'network-ethernet'
                      : 'network-general'
                  : undefined
              )}
            </div>
          )) ?? '—'}
        {/* and no VPN panel without a VPN to list */}
        {admin &&
          listedVPNs.length > 0 &&
          section(
            'VPN',
            <div className="space-y-4">
              {listedVPNs.map((provider) => {
                const data = extra[provider.id]!;
                if ('profiles' in data)
                  return (
                    <Fragment key={provider.id}>
                      {vpnProfiles(provider.id as 'openvpn' | 'wireguard', provider.name)}
                    </Fragment>
                  );
                const daemon = data;
                return (
                  <div key={provider.id}>
                    <button
                      type="button"
                      className="text-fg hover:text-info inline-flex items-center gap-2 border-0! bg-transparent! !p-0 text-sm font-medium"
                      onClick={() => navigate(`vpn-${provider.id}`)}
                    >
                      {provider.id === 'tailscale' ? (
                        <TailscaleIcon />
                      ) : provider.id === 'zerotier' ? (
                        <ZerotierIcon />
                      ) : (
                        <NetbirdIcon />
                      )}
                      {provider.name}
                    </button>
                    <div className="text-fg-muted mt-1 text-xs">
                      {state(daemon.state)}
                      {daemon.ip ? ` · ${daemon.ip}` : ''}
                    </div>
                  </div>
                );
              })}
            </div>,
            'vpn'
          )}
        {/* The UPS, once its driver reads one. */}
        {upsValues &&
          section(
            t('settings.netServices.ups.title'),
            <dl className="text-sm">
              {line(
                t('dashboard.ups.state'),
                upsState ? (
                  <StatusBadge tone={upsState[2]}>{t(`dashboard.ups.${upsState[1]}`)}</StatusBadge>
                ) : (
                  upsValues['ups.status']
                )
              )}
              {upsValues['battery.charge'] &&
                line(t('dashboard.ups.charge'), `${upsValues['battery.charge']} %`)}
              {Number(upsValues['battery.runtime']) > 0 &&
                line(
                  t('dashboard.ups.runtime'),
                  t('dashboard.ups.minutes', {
                    count: Math.round(Number(upsValues['battery.runtime']) / 60)
                  })
                )}
              {upsValues['ups.load'] && line(t('dashboard.ups.load'), `${upsValues['ups.load']} %`)}
              {upsValues['device.model'] &&
                line(t('dashboard.ups.model'), upsValues['device.model'])}
            </dl>,
            'services-ups'
          )}
        {/* The services that are switched on, each a way to its page. */}
        {enabledServices.length > 0 &&
          section(
            t('settings.services.title'),
            <div className="space-y-2 text-sm">
              {enabledServices.map((name) => {
                const status = extra[name]!;
                const neighbor = name === 'lldp' ? extra.lldp?.neighbors[0] : undefined;
                return (
                  <div key={name}>
                    <div className="flex items-center justify-between gap-2">
                      <button
                        type="button"
                        className="text-fg hover:text-info border-0! bg-transparent! !p-0 font-medium"
                        onClick={() => navigate(`services-${name}`)}
                      >
                        {t(`settings.netServices.${name}.title`)}
                      </button>
                      <StatusBadge tone={status.running ? 'success' : 'neutral'}>
                        {t(
                          status.running
                            ? 'settings.netServices.running'
                            : 'settings.netServices.notRunning'
                        )}
                      </StatusBadge>
                    </div>
                    {neighbor && (
                      <div className="text-fg-muted mt-1 text-xs break-words">
                        {neighbor.name || t('settings.netServices.lldp.unnamed')}
                        {neighbor.port
                          ? ` · ${t('settings.netServices.lldp.port', { port: neighbor.port })}`
                          : ''}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        {events.length > 0 &&
          section(
            t('dashboard.recentEvents'),
            <ul className="m-0 list-none space-y-2 p-0 text-sm">
              {events.map((entry) => (
                <li key={entry.seq} className="flex items-baseline justify-between gap-3">
                  <StatusBadge tone={journalTones[entry.severity] ?? 'neutral'}>
                    {entry.title}
                  </StatusBadge>
                  <span className="text-fg-muted shrink-0 text-xs">
                    {extra.time ? eventTime(entry.time) : ''}
                  </span>
                </li>
              ))}
            </ul>,
            'system-events'
          )}
        {/* The other NanoKVMs that announce themselves here, each a link to
            its own interface. */}
        {peers.length > 0 &&
          section(
            t('dashboard.peers'),
            <ul className="m-0 list-none space-y-2 p-0 text-sm">
              {peers.map((peer) => {
                const address = peer.addresses[0] ?? peer.host;
                const standard = peer.port === (peer.proto === 'http' ? 80 : 443);
                const url = `${peer.proto}://${address}${standard ? '' : `:${peer.port}`}/`;
                return (
                  <li key={`${peer.host}-${peer.port}-${peer.addresses.join(',')}`}>
                    <div className="flex items-baseline justify-between gap-3">
                      <a href={url} target="_blank" rel="noreferrer" className="font-medium">
                        {peer.name}
                      </a>
                      <span className="text-fg-muted shrink-0 text-xs">{peer.model}</span>
                    </div>
                    <div className="text-fg-muted mt-0.5 text-xs break-all">
                      {[peer.host, ...peer.addresses].join(' · ')}
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
        {section(
          t('dateTime.title'),
          <dl className="text-sm">
            {line(
              t('dashboard.deviceTime'),
              sys && extra.time
                ? formatDeviceTime(sys.now, extra.time.config, i18n.language, true)
                : '—'
            )}
            {line(t('dashboard.timezone'), extra.time?.config.timezone)}
            {line(
              t('dashboard.synchronization'),
              extra.time ? (
                <StatusBadge tone={extra.time.synchronized ? 'success' : 'danger'}>
                  {t(
                    extra.time.synchronized ? 'dashboard.synchronized' : 'dashboard.notSynchronized'
                  )}
                </StatusBadge>
              ) : (
                '—'
              )
            )}
            {line(t('dashboard.timeService'), extra.time?.daemon)}
            {line(t('dashboard.ntpServers'), extra.time?.config.servers.join(', '))}
          </dl>,
          admin ? 'system-date-time' : undefined
        )}
      </div>
    </div>
  );
};
