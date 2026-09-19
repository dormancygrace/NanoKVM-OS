import { useEffect, useRef, useState } from 'react';
import { Button, Checkbox, Divider, Input, Modal, Select, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/network.ts';

import { groupWifiNetworks, type WifiGroup } from './wifi-networks';
import { WifiSignal } from './wifi-signal';

type Pending = { until: number; enabled?: boolean; ssid?: string; band?: api.WifiBand };

export const Wifi = () => {
  const { t } = useTranslation();
  const tr = (key: string) => t(`settings.network.wifi.${key}`);
  function securityLabel(security: api.WifiNetwork['security']) {
    const labels = {
      wpa: 'WPA',
      'wpa-wpa2': 'WPA/WPA2',
      wpa2: 'WPA2',
      wpa3: 'WPA3',
      'wpa2-wpa3': 'WPA2/WPA3'
    };
    return security === 'open' || security === 'unsupported' ? tr(security) : labels[security];
  }
  const [state, setState] = useState<api.WifiStatus>();
  const autoScanStarted = useRef(false);
  const scanInFlight = useRef(false);
  const [networks, setNetworks] = useState<api.WifiNetwork[]>([]);
  const [scanned, setScanned] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<Pending>();
  const pendingRef = useRef<Pending | undefined>(undefined);
  const [message, setMessage] = useState('');
  const [modal, setModal] = useState(false);
  const [profileNetwork, setProfileNetwork] = useState<WifiGroup>();
  const [profile, setProfile] = useState<api.WifiProfile>({
    ssid: '',
    password: '',
    band: '2.4',
    hidden: false,
    security: 'wpa2-wpa3'
  });

  useEffect(() => {
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try {
        const rsp = await api.getWiFi();
        if (rsp.code !== 0) throw new Error();
        if (!alive) return;
        const next = rsp.data as api.WifiStatus;
        setState(next);
        setMessage((old) => (old === 'statusFailed' ? '' : old));
        const operation = pendingRef.current;
        if (operation && !next.busy) {
          const done =
            operation.ssid !== undefined
              ? next.connected && next.ssid === operation.ssid && next.band === operation.band
              : next.enabled === operation.enabled;
          if (next.error || done) {
            pendingRef.current = undefined;
            setPending(undefined);
            setMessage(next.error ? 'operationFailed' : '');
          }
        }
      } catch {
        if (alive && !pendingRef.current) setMessage('statusFailed');
      } finally {
        if (alive) {
          if (pendingRef.current && Date.now() > pendingRef.current.until) {
            pendingRef.current = undefined;
            setPending(undefined);
            setMessage('connectionTimeout');
          }
          timer = setTimeout(refresh, 3000);
        }
      }
    }
    void refresh();
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, []);

  const locked = busy || !!pending || !!state?.busy;
  const enabled = pending?.enabled ?? state?.enabled ?? true;
  const canConfigure = !!state?.supported && !state.apMode && enabled;

  function track(operation: Omit<Pending, 'until'>) {
    const next = { ...operation, until: Date.now() + 60000 };
    pendingRef.current = next;
    setPending(next);
  }

  async function toggle(value: boolean) {
    if (locked) return;
    setBusy(true);
    setMessage('');
    try {
      const rsp = await api.setWifiEnabled(value);
      if (rsp.code !== 0) throw new Error();
      track({ enabled: value });
      setNetworks([]);
      setScanned(false);
    } catch {
      setMessage('operationFailed');
    } finally {
      setBusy(false);
    }
  }

  async function scan() {
    if (locked || scanInFlight.current || !canConfigure || !state?.bands?.length) return;
    scanInFlight.current = true;
    setBusy(true);
    setScanning(true);
    setMessage('');
    try {
      const rsp = await api.scanWifi('all');
      if (rsp.code !== 0) throw new Error();
      setNetworks(rsp.data);
      setScanned(true);
    } catch {
      setMessage('scanFailed');
    } finally {
      setBusy(false);
      setScanning(false);
      scanInFlight.current = false;
    }
  }

  // Refresh only while this panel is visible; never overlap radio operations.
  const scanLatest = useRef(scan);
  scanLatest.current = scan;
  useEffect(() => {
    if (!canConfigure) autoScanStarted.current = false;
    if (canConfigure && state?.bands?.length && !locked && !modal && !autoScanStarted.current) {
      autoScanStarted.current = true;
      void scanLatest.current();
    }
  }, [canConfigure, state?.bands?.length, locked, modal]);
  const scanAllowed = useRef(false);
  scanAllowed.current = canConfigure && !locked && !modal;
  useEffect(() => {
    const timer = setInterval(() => {
      if (document.visibilityState === 'visible' && scanAllowed.current) {
        void scanLatest.current();
      }
    }, 30000);
    return () => clearInterval(timer);
  }, []);

  const groupedNetworks = groupWifiNetworks(networks);

  function open(network?: WifiGroup) {
    setProfileNetwork(network);
    const candidate =
      network?.candidates.find(
        (item) => state?.connected && state.ssid === item.ssid && state.band === item.band
      ) || network;
    setProfile({
      ssid: network?.ssid || '',
      password: '',
      band: candidate?.band || state?.band || state?.bands?.[0] || '2.4',
      hidden: false,
      security: candidate && candidate.security !== 'unsupported' ? candidate.security : 'wpa2-wpa3'
    });
    setMessage('');
    setModal(true);
  }

  const valid =
    new TextEncoder().encode(profile.ssid).length > 0 &&
    new TextEncoder().encode(profile.ssid).length <= 32 &&
    !/[\r\n\0]/.test(profile.ssid) &&
    (profile.security === 'open' ||
      (new TextEncoder().encode(profile.password).length >= 8 &&
        new TextEncoder().encode(profile.password).length <= 63 &&
        !/[\r\n\0]/.test(profile.password)));

  async function connect() {
    if (locked || !valid) return;
    setBusy(true);
    setMessage('');
    try {
      const rsp = await api.configureWifi(profile);
      if (rsp.code !== 0) throw new Error();
      track({ ssid: profile.ssid, band: profile.band });
      setProfile((old) => ({ ...old, password: '' }));
      setModal(false);
    } catch {
      setMessage('operationFailed');
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1 text-base">
        <span>{tr('title')}</span>
        {state && (!state.supported || state.model) && (
          <span className="text-xs text-neutral-500">
            {state.supported ? state.model : 'not detected'}
          </span>
        )}
      </div>
      <Divider className="opacity-50" />
      <div className="flex flex-col space-y-5">
        <div className="flex items-center justify-between space-x-3">
          <div className="flex min-w-0 flex-col space-y-1">
            <span>{tr('title')}</span>
            <span className="text-xs break-words text-neutral-500">
              {!state
                ? tr('loading')
                : !state.supported
                  ? ''
                  : state.apMode
                    ? tr('apMode')
                    : state.connected && enabled
                      ? state.ssid
                      : enabled
                        ? tr('description')
                        : tr('disabled')}
            </span>
          </div>
          <Switch
            aria-label={tr('title')}
            checked={enabled}
            loading={(busy && !scanning && !modal) || !!pending}
            disabled={!state?.supported || state.apMode || locked}
            onChange={toggle}
          />
        </div>

        {canConfigure && (
          <>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <span className="text-sm">{tr('availableNetworks')}</span>
              <Button
                size="small"
                onClick={scan}
                loading={scanning}
                disabled={locked || !state.bands?.length}
              >
                {tr('scan')}
              </Button>
            </div>
            {!state.bands?.length && (
              <span className="text-xs text-neutral-500">{tr('bandsUnavailable')}</span>
            )}
            <div className="flex flex-col space-y-3">
              {groupedNetworks.map((network) => (
                <div
                  key={`${network.ssid}-${network.security}`}
                  className="flex items-center justify-between gap-3"
                >
                  <div className="flex min-w-0 flex-1 items-center gap-3">
                    <WifiSignal signal={network.signal} />
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2 text-sm">
                        <span className="break-all">{network.ssid}</span>
                        <span className="rounded border border-neutral-600 px-1.5 py-0.5 text-xs text-neutral-400">
                          {network.bands.join(' / ')} GHz
                        </span>
                      </div>
                      <div className="text-xs text-neutral-500">
                        {Array.from(
                          new Set(
                            network.candidates.flatMap((item) =>
                              securityLabel(item.security).split('/')
                            )
                          )
                        ).join(' / ')}
                        {' · '}
                        {Math.round(network.signal)} dBm
                      </div>
                    </div>
                  </div>
                  <Button
                    size="small"
                    disabled={locked || network.security === 'unsupported'}
                    onClick={() => open(network)}
                  >
                    {state.connected &&
                    state.ssid === network.ssid &&
                    !!state.band &&
                    network.bands.includes(state.band)
                      ? tr('reconnect')
                      : tr('joinBtn')}
                  </Button>
                </div>
              ))}
              {scanned && networks.length === 0 && (
                <span className="text-xs text-neutral-500">{tr('noNetworks')}</span>
              )}
            </div>
            <Button
              size="small"
              className="self-start"
              disabled={locked || !state.bands?.length}
              onClick={() => open()}
            >
              {tr('manual')}
            </Button>
          </>
        )}
        {!!pending && (
          <span role="status" className="text-xs text-neutral-500">
            {tr('applying')}
          </span>
        )}
        {message && (
          <span role="alert" className="text-xs text-red-500">
            {tr(message)}
          </span>
        )}

        <Modal
          title={tr('connect')}
          open={modal}
          centered
          onOk={connect}
          onCancel={() => {
            if (!busy) {
              setModal(false);
              setProfile((old) => ({ ...old, password: '' }));
            }
          }}
          okText={tr('joinBtn')}
          cancelText={tr('cancelBtn')}
          confirmLoading={busy}
          okButtonProps={{ disabled: locked || !valid }}
          cancelButtonProps={{ disabled: busy }}
          closable={!busy}
        >
          <div className="flex flex-col space-y-3 py-4">
            <label className="flex flex-col gap-1 text-sm">
              {tr('ssid')}
              <Input
                value={profile.ssid}
                disabled={busy}
                autoComplete="off"
                onChange={(e) => setProfile({ ...profile, ssid: e.target.value })}
              />
            </label>
            <Checkbox
              checked={profile.hidden}
              disabled={busy}
              onChange={(e) => setProfile({ ...profile, hidden: e.target.checked })}
            >
              {tr('hidden')}
            </Checkbox>
            <label className="flex flex-col gap-1 text-sm">
              {tr('security')}
              <Select
                value={profile.security}
                disabled={busy}
                options={[
                  ...(['wpa2-wpa3', 'wpa3', 'wpa2', 'wpa-wpa2', 'wpa'] as const).map((value) => ({
                    value,
                    label: `${securityLabel(value)} Personal`
                  })),
                  { value: 'open', label: tr('open') }
                ]}
                onChange={(security) => setProfile({ ...profile, security, password: '' })}
              />
            </label>
            {profile.security !== 'open' && (
              <label className="flex flex-col gap-1 text-sm">
                {tr('password')}
                <Input.Password
                  value={profile.password}
                  disabled={busy}
                  autoComplete="new-password"
                  onChange={(e) => setProfile({ ...profile, password: e.target.value })}
                />
                <span className="text-xs text-neutral-500">{tr('passwordHint')}</span>
              </label>
            )}
            <label className="flex flex-col gap-1 text-sm">
              {tr('band')}
              <Select
                value={profile.band}
                disabled={busy}
                options={(state?.bands || []).map((value) => ({
                  value,
                  label: value === '2.4' ? tr('band24') : tr('band5')
                }))}
                onChange={(band: api.WifiBand) => {
                  const candidate = profileNetwork?.candidates.find(
                    (item) =>
                      item.ssid === profile.ssid &&
                      item.band === band &&
                      item.security !== 'unsupported'
                  );
                  setProfile({
                    ...profile,
                    band,
                    security:
                      candidate && candidate.security !== 'unsupported'
                        ? candidate.security
                        : profile.security
                  });
                }}
              />
            </label>
            {message && (
              <span role="alert" className="text-xs text-red-500">
                {tr(message)}
              </span>
            )}
          </div>
        </Modal>
      </div>
    </>
  );
};
