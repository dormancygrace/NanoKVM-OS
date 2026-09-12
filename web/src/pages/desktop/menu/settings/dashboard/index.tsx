import { useEffect, useRef, useState, type ReactNode } from 'react';
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
import { OpenVPNIcon } from '@/components/icons/openvpn';
import { Tailscale as TailscaleIcon } from '@/components/icons/tailscale';
import { WireGuardIcon } from '@/components/icons/wireguard';

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
    load: string[] | null;
    temperature: number | null;
    cpuFrequency: number | null;
    storage: Disk[];
    interfaces: Interface[];
  };
  memory: MemoryStatus | null;
  application: string;
  image: string;
};
type Video = {
  inputWidth: number;
  inputHeight: number;
  outputWidth: number;
  outputHeight: number;
  fps: number;
  measuredFps: number;
  effectiveFps: number;
  bitRate: number;
  quality: number;
  gop: number;
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
type Extras = {
  time?: { config: DateTimeConfig; synchronized: boolean; daemon: string };
  wifi?: { connected: boolean; ssid: string };
  wireguard?: { profiles: Profile[]; now?: number };
  openvpn?: { profiles: Profile[] };
  tailscale?: { state: string; ip: string; name: string };
};
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

export const Dashboard = ({ navigate }: { navigate: (tab: string) => void }) => {
  const { t, i18n } = useTranslation();
  const { account } = useAuth();
  const admin = account.role === 'admin';
  const [data, setData] = useState<Snapshot>();
  const [video, setVideo] = useState<Video>();
  const [extra, setExtra] = useState<Extras>({});
  const [cpu, setCpu] = useState<number | null>(null);
  const [errors, setErrors] = useState<string[]>([]);
  const [extraFailed, setExtraFailed] = useState(false);
  const previous = useRef<CPU | null>(null);
  const enabled = useAtomValue(isHdmiEnabledAtom);
  const ready = useAtomValue(captureReadyAtom);
  const mode = useAtomValue(videoModeAtom);
  const sessions = useAtomValue(videoSessionCountAtom);

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
          const next = snapshot.system.cpu,
            last = previous.current;
          const total = next && last ? next.total - last.total : 0;
          const idle = next && last ? next.idle - last.idle : 0;
          setCpu(total > 0 && idle >= 0 && idle <= total ? ((total - idle) / total) * 100 : null);
          previous.current = next;
          report('system', false);
        } catch {
          if (disposed) return;
          setCpu(null);
          previous.current = null;
          report('system', true);
        }
      } else previous.current = null;
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
            ['wireguard', '/api/extensions/wireguard/status']
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
  }, [admin]);

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
  const state = (key: string) => t(`dashboard.states.${key}`, { defaultValue: key });
  const line = (label: string, value: ReactNode) => (
    <div className="flex flex-wrap justify-between gap-x-4 gap-y-1 py-1">
      <dt className="text-neutral-400">{label}</dt>
      <dd className="min-w-0 break-words text-right text-neutral-200">{value ?? '—'}</dd>
    </div>
  );
  const section = (title: string, children: ReactNode, tab?: string) => (
    <section className="min-w-0 rounded-xl border border-neutral-700/60 bg-neutral-800/30 p-4">
      <div className="mb-3 flex items-center justify-between gap-2">
        <h3 className="font-medium">{title}</h3>
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
    </section>
  );
  const metric = (
    title: string,
    value: string,
    icon: ReactNode,
    detail?: string,
    fill?: number
  ) => (
    <div className="min-w-0 rounded-xl border border-neutral-700/60 bg-neutral-800/40 p-3">
      <div className="mb-2 flex items-center gap-2 text-xs text-neutral-400">
        {icon}
        {title}
      </div>
      <div className="break-words text-lg font-medium tabular-nums">{value}</div>
      {detail && <div className="mt-1 text-xs text-neutral-500">{detail}</div>}
      {fill !== undefined && (
        <Progress
          percent={fill}
          showInfo={false}
          size="small"
          strokeColor="#38bdf8"
          trailColor="#404040"
        />
      )}
    </div>
  );
  const vpnProfiles = (kind: 'openvpn' | 'wireguard', name: string) => {
    const profiles = extra[kind]?.profiles;
    return (
      <div>
        <button
          type="button"
          className="inline-flex items-center gap-2 !border-0 !bg-transparent !p-0 text-sm font-medium text-neutral-200 hover:text-blue-400"
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
                  <span
                    className={
                      profile.state === 'connected' ? 'text-emerald-400' : 'text-neutral-400'
                    }
                  >
                    {state(profile.state)}
                  </span>
                </div>
                {profile.address && (
                  <div className="mt-1 break-all text-neutral-400">{profile.address}</div>
                )}
                {!!(profile.mtu || iface?.mtu) && (
                  <div className="mt-1 text-neutral-500">MTU {profile.mtu || iface?.mtu}</div>
                )}
                {kind === 'wireguard' && profile.state !== 'off' && (
                  <div className="mt-1 text-neutral-500">
                    RX {bytes(profile.received)} · TX {bytes(profile.sent)}
                  </div>
                )}
                {!!profile.lastHandshake && (
                  <div className="mt-1 text-neutral-500">
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
          <div className="mt-1 text-xs text-neutral-500">
            {profiles ? t('dashboard.noProfiles') : '—'}
          </div>
        )}
      </div>
    );
  };
  return (
    <div className="space-y-5 pb-6">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h2 className="text-xl font-medium">Dashboard</h2>
        </div>
        <span className="text-xs text-neutral-500">{t('dashboard.live')}</span>
      </div>
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
          cpu ?? undefined
        )}
        {metric(
          'RAM',
          bytes(memory?.usedBytes),
          <MemoryStickIcon size={16} />,
          memory ? t('dashboard.of', { total: bytes(memory.totalBytes) }) : undefined,
          memory ? percent(memory.usedBytes, memory.totalBytes) : undefined
        )}
        {metric(
          t('dashboard.freeStorage'),
          bytes(disk?.free),
          <HardDriveIcon size={16} />,
          disk ? t('dashboard.of', { total: bytes(disk.total) }) : undefined,
          disk ? percent(disk.free, disk.total) : undefined
        )}
        {metric(t('dashboard.uptime'), uptime, <TimerIcon size={16} />)}
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        {section(
          t('dashboard.device'),
          <dl className="text-sm">
            {line(t('settings.about.hostname'), sys?.hostname)}
            {line(
              t('dashboard.application'),
              data?.application ? formatVersion(data.application) : undefined
            )}
            {line(t('settings.about.image'), data?.image)}
            {line(t('dashboard.kernel'), sys?.kernel)}
            {line(t('dashboard.load'), sys?.load?.join(' / '))}
          </dl>,
          admin ? 'device' : undefined
        )}
        {section(
          t('videoSettings.title'),
          <dl className="text-sm">
            {line(
              t('dashboard.capture'),
              t(
                !ready
                  ? 'capture.checking'
                  : enabled
                    ? 'videoSettings.captureOn'
                    : 'videoSettings.captureOff'
              )
            )}
            {line(
              t('videoSettings.input'),
              enabled ? size(video?.inputWidth, video?.inputHeight) : '—'
            )}
            {line(
              t('videoSettings.output'),
              enabled ? size(video?.outputWidth, video?.outputHeight) : '—'
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
              video ? `${enabled ? video.measuredFps : 0} / ${video.fps} FPS` : '—'
            )}
            {line(
              t(mode === 'mjpeg' ? 'screen.quality' : 'videoSettings.bitrate'),
              video
                ? mode === 'mjpeg'
                  ? `${video.quality}%`
                  : `${video.bitRate / 1000} Mbit/s`
                : '—'
            )}
            {line(t('dashboard.sessions'), sessions)}
          </dl>,
          'video'
        )}
        {section(
          t('dashboard.memory'),
          <dl className="text-sm">
            {line(t('dashboard.available'), bytes(memory?.availableBytes))}
            {line(t('dashboard.cache'), bytes(memory?.cachedBytes))}
            {line(
              'zram',
              memory
                ? memory.zram.enabled
                  ? `${bytes(memory.zram.usedBytes)} / ${memory.zram.sizeMiB} MiB`
                  : state('off')
                : '—'
            )}
            {memory?.zram.enabled &&
              line(
                t('dashboard.compression'),
                `${memory.zram.algorithm}${memory.zram.recompressReady ? ' + zstd' : ''}`
              )}
            {line(
              'Swap (SD)',
              memory
                ? memory.sd.enabled
                  ? `${bytes(memory.sd.usedBytes)} / ${memory.sd.sizeMiB} MiB`
                  : state('off')
                : '—'
            )}
          </dl>,
          admin ? 'memory' : undefined
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
                    <span className="text-xs text-neutral-500">{item.path}</span>
                  </span>
                  {item.readOnly && (
                    <span className="text-xs text-amber-300">{t('dashboard.readOnly')}</span>
                  )}
                </div>
                {item.available ? (
                  <>
                    <Progress
                      size="small"
                      percent={percent(item.used, item.total)}
                      showInfo={false}
                      strokeColor="#38bdf8"
                      trailColor="#404040"
                    />
                    <div className="text-xs text-neutral-400">
                      {t('dashboard.diskSpace', {
                        free: bytes(item.free),
                        total: bytes(item.total)
                      })}
                    </div>
                  </>
                ) : (
                  <div className="mt-1 text-xs text-neutral-500">{t('dashboard.unavailable')}</div>
                )}
              </div>
            )) ?? '—'}
          </div>
        )}
      </div>
      {section(
        t('settings.network.title'),
        <div className="grid gap-4 sm:grid-cols-2">
          {sys?.interfaces
            .filter(
              (iface) =>
                !admin ||
                !(iface.kind === 'wireguard' || iface.name === 'tailscale0' || iface.kind === 'tun')
            )
            .map((iface) => (
              <div key={iface.name} className="min-w-0 text-sm">
                <div className="mb-2 flex items-center gap-2">
                  {iface.kind === 'wireguard' ? (
                    <WireGuardIcon />
                  ) : iface.wireless ? (
                    <WifiIcon size={16} />
                  ) : (
                    <EthernetPortIcon size={16} />
                  )}
                  <span className="min-w-0 break-words" title={iface.name}>
                    {iface.kind === 'wireguard'
                      ? wireguardNames.get(iface.name) || 'WireGuard'
                      : iface.name}
                  </span>
                  <span
                    className={`ml-auto text-xs ${iface.up && iface.connected ? 'text-emerald-400' : 'text-neutral-500'}`}
                  >
                    {t(
                      iface.up && iface.connected ? 'dashboard.connected' : 'dashboard.disconnected'
                    )}
                  </span>
                </div>
                {iface.kind === 'wireguard' && (
                  <div className="mb-1 text-xs text-neutral-500">
                    {wireguardNames.get(iface.name) ? 'WireGuard' : iface.name}
                  </div>
                )}
                {iface.wireless && iface.connected && extra.wifi?.connected && (
                  <p className="mb-1 break-all text-neutral-300">{extra.wifi.ssid}</p>
                )}
                <div className={iface.connected ? 'text-neutral-300' : 'text-neutral-500'}>
                  {iface.addresses.map((addr) => (
                    <div key={addr} className="break-all">
                      {addr}
                    </div>
                  ))}
                </div>
                <div className="mt-2 text-xs text-neutral-500">
                  MTU {iface.mtu}
                  {iface.mac ? ` · ${iface.mac}` : ''}
                </div>
                <div className="mt-1 text-xs text-neutral-500">
                  RX {bytes(iface.received)} · TX {bytes(iface.sent)}
                </div>
              </div>
            )) ?? '—'}
        </div>,
        admin ? 'network' : undefined
      )}
      <div className="grid gap-4 md:grid-cols-2">
        {admin &&
          section(
            'VPN',
            <div className="space-y-4">
              {vpnProfiles('openvpn', 'OpenVPN')}
              <div>
                <button
                  type="button"
                  className="inline-flex items-center gap-2 !border-0 !bg-transparent !p-0 text-sm font-medium text-neutral-200 hover:text-blue-400"
                  onClick={() => navigate('vpn-tailscale')}
                >
                  <TailscaleIcon />
                  Tailscale
                </button>
                <div className="mt-1 text-xs text-neutral-400">
                  {extra.tailscale ? state(extra.tailscale.state) : '—'}
                  {extra.tailscale?.ip ? ` · ${extra.tailscale.ip}` : ''}
                </div>
              </div>
              {vpnProfiles('wireguard', 'WireGuard')}
            </div>
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
              extra.time
                ? t(
                    extra.time.synchronized ? 'dashboard.synchronized' : 'dashboard.notSynchronized'
                  )
                : '—'
            )}
            {line(t('dashboard.timeService'), extra.time?.daemon)}
            {line(t('dashboard.ntpServers'), extra.time?.config.servers.join(', '))}
          </dl>,
          admin ? 'date-time' : undefined
        )}
      </div>
    </div>
  );
};
