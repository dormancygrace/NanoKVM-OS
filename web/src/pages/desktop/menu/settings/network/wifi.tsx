import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Checkbox, Input, message, Modal, Select, Switch } from 'antd';
import i18n from 'i18next';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/network.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { Panel, SettingRow, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

import { groupWifiNetworks, type WifiGroup } from './wifi-networks';
import { WifiSignal } from './wifi-signal';

type Pending = { until: number; enabled?: boolean; ssid?: string };

// Failures of a radio operation that only the status poll can detect.
function notifyFailure(key: 'operationFailed' | 'connectionTimeout') {
  void message.error(i18n.t(`settings.network.wifi.${key}`));
}

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
  const [statusFailed, setStatusFailed] = useState(false);
  const [modal, setModal] = useState(false);
  const [profile, setProfile] = useState<api.WifiProfile>({
    ssid: '',
    password: '',
    band: '2.4',
    hidden: false,
    security: 'wpa2-wpa3'
  });

  useEffect(() => {
    let alive = true;
    let inFlight = false;
    async function refresh() {
      if (!alive || inFlight) return;
      inFlight = true;
      try {
        const rsp = await api.getWiFi();
        if (rsp.code !== 0) throw new Error();
        if (!alive) return;
        const next = rsp.data as api.WifiStatus;
        setState(next);
        setStatusFailed(false);
        const operation = pendingRef.current;
        if (operation && !next.busy) {
          const done =
            operation.ssid !== undefined
              ? next.connected && next.ssid === operation.ssid
              : next.enabled === operation.enabled;
          if (next.error || done) {
            pendingRef.current = undefined;
            setPending(undefined);
            if (next.error) notifyFailure('operationFailed');
          }
        }
      } catch {
        if (alive && !pendingRef.current) setStatusFailed(true);
      } finally {
        inFlight = false;
        if (alive && pendingRef.current && Date.now() > pendingRef.current.until) {
          pendingRef.current = undefined;
          setPending(undefined);
          notifyFailure('connectionTimeout');
        }
      }
    }
    void refresh();
    const stopPolling = pollWhileVisible(() => void refresh(), 3000);
    return () => {
      alive = false;
      stopPolling();
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
    if (
      !value &&
      !(await confirmAction({
        title: tr('confirmDisable'),
        content: tr('confirmDisableDescription'),
        danger: true
      }))
    )
      return;
    setBusy(true);
    try {
      const rsp = await api.setWifiEnabled(value);
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.network.wifi.operationFailed');
        return;
      }
      track({ enabled: value });
      setNetworks([]);
      setScanned(false);
    } catch (err) {
      showRequestError(err, 'settings.network.wifi.operationFailed');
    } finally {
      setBusy(false);
    }
  }

  async function setPreferredBand(preferredBand: api.WifiBand) {
    if (locked || !state) return;
    setBusy(true);
    try {
      const rsp = await api.setWifiBandPreference(preferredBand);
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.network.wifi.operationFailed');
        return;
      }
      // Changing a preference must not interrupt the connection currently
      // carrying this request. It is used by the next association/restart.
      setState((old) => (old ? { ...old, preferredBand } : old));
    } catch (err) {
      showRequestError(err, 'settings.network.wifi.operationFailed');
    } finally {
      setBusy(false);
    }
  }

  async function scan() {
    if (locked || scanInFlight.current || !canConfigure || !state?.bands?.length) return;
    scanInFlight.current = true;
    setBusy(true);
    setScanning(true);
    try {
      const rsp = await api.scanWifi('all');
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.network.wifi.scanFailed');
        return;
      }
      setNetworks(rsp.data);
      setScanned(true);
    } catch (err) {
      showRequestError(err, 'settings.network.wifi.scanFailed');
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
    const candidate =
      network?.candidates.find(
        (item) => state?.connected && state.ssid === item.ssid && state.band === item.band
      ) || network;
    setProfile({
      ssid: network?.ssid || '',
      password: '',
      band: state?.preferredBand || candidate?.band || state?.band || state?.bands?.[0] || '5',
      hidden: false,
      security: candidate && candidate.security !== 'unsupported' ? candidate.security : 'wpa2-wpa3'
    });
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
    try {
      const rsp = await api.configureWifi({ ...profile, preferredBand: state?.preferredBand });
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.network.wifi.operationFailed');
        return;
      }
      track({ ssid: profile.ssid });
      setProfile((old) => ({ ...old, password: '' }));
      setModal(false);
    } catch (err) {
      showRequestError(err, 'settings.network.wifi.operationFailed');
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-6">
      {statusFailed && <Alert type="error" showIcon message={tr('statusFailed')} />}

      <SettingsSection
        description={
          state && (!state.supported || state.model)
            ? state.supported
              ? state.model
              : tr('notDetected')
            : undefined
        }
      >
        <SettingRow
          label={tr('title')}
          description={
            !state ? (
              tr('loading')
            ) : !state.supported ? (
              ''
            ) : state.apMode ? (
              tr('apMode')
            ) : state.connected && enabled ? (
              <StatusBadge tone="success">
                <span className="break-words">{state.ssid}</span>
              </StatusBadge>
            ) : enabled ? (
              tr('description')
            ) : (
              <StatusBadge tone="neutral">{tr('disabled')}</StatusBadge>
            )
          }
          htmlFor="wifi-enabled"
        >
          <Switch
            id="wifi-enabled"
            aria-label={tr('title')}
            aria-describedby="wifi-enabled-description"
            checked={enabled}
            loading={(busy && !scanning && !modal) || !!pending}
            disabled={!state?.supported || state.apMode || locked}
            onChange={toggle}
          />
        </SettingRow>

        {canConfigure && (
          <SettingRow
            label={tr('preferredBand')}
            description={tr('preferredBandHint')}
            htmlFor="wifi-preferred-band"
          >
            <Select
              id="wifi-preferred-band"
              aria-describedby="wifi-preferred-band-description"
              style={{ width: 180 }}
              value={state.preferredBand}
              disabled={locked}
              options={(['2.4', '5'] as const).map((value) => ({
                value,
                label: value === '2.4' ? tr('band24') : tr('band5'),
                disabled: !state.bands.includes(value)
              }))}
              onChange={setPreferredBand}
            />
          </SettingRow>
        )}

        {!!pending && (
          <div role="status" className="text-fg-muted text-xs">
            {tr('applying')}
          </div>
        )}
      </SettingsSection>

      {canConfigure && (
        <SettingsSection
          title={tr('availableNetworks')}
          actions={
            <Button
              size="small"
              onClick={scan}
              loading={scanning}
              disabled={locked || !state.bands?.length}
            >
              {tr('scan')}
            </Button>
          }
        >
          {!state.bands?.length && (
            <div className="text-fg-muted text-xs">{tr('bandsUnavailable')}</div>
          )}
          {groupedNetworks.length > 0 && (
            <Panel flush className="divide-line divide-y">
              {groupedNetworks.map((network) => (
                <div
                  key={`${network.ssid}-${network.security}`}
                  className="flex items-center justify-between gap-3 px-4 py-3"
                >
                  <div className="flex min-w-0 flex-1 items-center gap-3">
                    <WifiSignal signal={network.signal} />
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2 text-sm">
                        <span className="break-all">{network.ssid}</span>
                        <span className="border-line text-fg-muted rounded border px-1.5 py-0.5 text-xs">
                          {network.bands.join(' / ')} GHz
                        </span>
                      </div>
                      <div className="text-fg-muted text-xs">
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
            </Panel>
          )}
          {scanned && networks.length === 0 && (
            <div className="text-fg-muted text-xs">{tr('noNetworks')}</div>
          )}
          <div>
            <Button size="small" disabled={locked || !state.bands?.length} onClick={() => open()}>
              {tr('manual')}
            </Button>
          </div>
        </SettingsSection>
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
              <span className="text-fg-muted text-xs">{tr('passwordHint')}</span>
            </label>
          )}
        </div>
      </Modal>
    </div>
  );
};
