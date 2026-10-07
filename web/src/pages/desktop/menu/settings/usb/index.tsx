import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Collapse, message, Select, Switch, Tag, Tooltip } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/virtual-device.ts';
import {
  canToggleDevice,
  endpointUsage,
  fitsBudget,
  matchingPreset,
  normalizeUsbStatus,
  sameComposition,
  toggleDevice,
  usbDevices,
  usbPresets
} from '@/lib/usb-composition.ts';
import type { UsbComposition, UsbDevice, UsbStatus } from '@/lib/usb-composition.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { Panel, SettingRow } from '@/components/ui/settings.tsx';

import { MouseJiggler } from '../device/mouse-jiggler';

const previousCompositionKey = 'nanokvm.usb.previous-composition';
function readPreviousComposition(): UsbComposition | undefined {
  try {
    const value = JSON.parse(localStorage.getItem(previousCompositionKey) ?? 'null');
    if (
      !value ||
      !['normal', 'hid-only'].includes(value.mode) ||
      !usbDevices.every((name) => typeof value[name] === 'boolean') ||
      !usbDevices.some((name) => value[name]) ||
      (value.mode === 'hid-only' && (value.network || value.disk || value.audio))
    )
      return;
    return value;
  } catch {
    return;
  }
}

export const Usb = () => {
  const { t } = useTranslation();
  const [status, setStatus] = useState<UsbStatus>();
  const [draft, setDraft] = useState<UsbComposition>();
  const [selection, setSelection] = useState('custom');
  const [manualOpen, setManualOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const busy = useRef(false);
  const [error, setError] = useState('');
  const [loadFailed, setLoadFailed] = useState(false);
  const previousComposition = useRef<UsbComposition | undefined>(readPreviousComposition());

  const adopt = useCallback((next: UsbStatus) => {
    setLoadFailed(false);
    setStatus(next);
    setDraft(next);
    if (usbDevices.some((name) => next[name])) {
      previousComposition.current = { ...next };
      try {
        localStorage.setItem(previousCompositionKey, JSON.stringify(next));
      } catch {
        /* Keep the session copy when storage is unavailable. */
      }
    }
    const preset = matchingPreset(next);
    setSelection(preset);
    if (preset === 'custom') setManualOpen(true);
  }, []);

  const fetchStatus = useCallback(async () => {
    const rsp = await api.getVirtualDevice();
    if (rsp.code !== 0) throw new Error(rsp.msg);
    return normalizeUsbStatus(rsp.data);
  }, []);

  useEffect(() => {
    let active = true;
    fetchStatus()
      .then((next) => {
        if (active) adopt(next);
      })
      .catch(() => {
        if (active) {
          setError(t('settings.usb.loadFailed'));
          setLoadFailed(true);
        }
      });
    return () => {
      active = false;
    };
  }, [adopt, fetchStatus, t]);

  function choosePreset(id: string) {
    if (busy.current) return;
    setSelection(id);
    if (id === 'custom') {
      setManualOpen(true);
    } else {
      const preset = usbPresets.find((item) => item.id === id);
      if (preset && status && fitsBudget(preset.composition, status))
        setDraft({ ...preset.composition, pointerProfile: draft?.pointerProfile });
    }
    setError('');
  }

  function toggle(name: UsbDevice) {
    if (!status || !draft || busy.current || !canToggleDevice(draft, name, status)) return;
    setDraft(toggleDevice(draft, name));
    setSelection('custom');
    setError('');
  }

  async function toggleUsb(enabled: boolean) {
    if (!status?.revision || busy.current) return;
    let next: UsbComposition;
    if (enabled) {
      next = { ...(previousComposition.current ?? usbPresets[0].composition) };
      if (!fitsBudget(next, status)) return;
    } else {
      const confirmed = await confirmAction({
        title: t('settings.usb.confirmDisable'),
        content: t('settings.usb.confirmDisableDescription'),
        danger: true
      });
      if (!confirmed) return;
      previousComposition.current = { ...status };
      next = {
        mode: 'normal',
        keyboard: false,
        relative: false,
        absolute: false,
        network: false,
        disk: false,
        serial: false,
        audio: false
      };
    }
    await saveComposition(next);
  }

  async function apply() {
    if (draft) await saveComposition(draft);
  }

  async function saveComposition(requested: UsbComposition) {
    if (!status?.revision || busy.current || !fitsBudget(requested, status)) return;
    busy.current = true;
    setLoading(true);
    setError('');
    try {
      const rsp = await api.setUsbComposition(requested, status.revision);
      if (rsp.code === 0) {
        adopt(normalizeUsbStatus(rsp.data));
      } else {
        adopt(await fetchStatus());
        const reason = t(rsp.code === -5 ? 'settings.usb.changed' : 'settings.usb.updateFailed');
        setError(reason);
        message.error(reason);
      }
    } catch {
      // A USB network disconnect can lose a successful response. Read back;
      // never blindly replay a mutation after an uncertain result.
      try {
        const next = await fetchStatus();
        adopt(next);
        if (!sameComposition(next, requested)) {
          setError(t('settings.usb.updateFailed'));
          message.error(t('settings.usb.updateFailed'));
        }
      } catch {
        setError(t('settings.usb.reconnectFailed'));
        message.error(t('settings.usb.reconnectFailed'));
      }
    } finally {
      window.dispatchEvent(new Event('nanokvm:usb-updated'));
      busy.current = false;
      setLoading(false);
    }
  }

  async function reload() {
    if (busy.current) return;
    try {
      adopt(await fetchStatus());
      setError('');
    } catch {
      setError(t('settings.usb.loadFailed'));
      setLoadFailed(true);
    }
  }

  const used = draft && status ? endpointUsage(draft, status.costs) : undefined;
  const dirty = Boolean(draft && status && !sameComposition(draft, status));
  const enabled = Boolean(status && usbDevices.some((name) => status[name]));
  const empty = Boolean(draft && !usbDevices.some((name) => draft[name]));
  const withinBudget = Boolean(draft && status && fitsBudget(draft, status));

  return (
    <div className="space-y-6">
      {loadFailed && <Alert type="error" showIcon message={t('settings.usb.loadFailed')} />}
      <SettingRow label={t('settings.usb.enabled')} htmlFor="usb-enabled">
        <Switch
          id="usb-enabled"
          checked={enabled}
          disabled={!status?.revision || loading}
          loading={loading || (!status && !error)}
          onChange={(next) => void toggleUsb(next)}
        />
      </SettingRow>
      {enabled && (
        <>
          <div className="space-y-4">
            <SettingRow
              label={t('settings.usb.presetLabel')}
              description={
                selection === 'custom'
                  ? t('settings.usb.customDescription')
                  : t(`settings.usb.presets.${selection}.description`)
              }
              htmlFor="usb-preset"
              stacked
            >
              <Select
                id="usb-preset"
                aria-describedby="usb-preset-description"
                className="w-full"
                value={selection}
                loading={!status && !error}
                disabled={!status || loading}
                onChange={choosePreset}
                options={[
                  ...usbPresets.map((preset) => ({
                    value: preset.id,
                    label: t(`settings.usb.presets.${preset.id}.title`),
                    disabled: !status || !fitsBudget(preset.composition, status)
                  })),
                  { value: 'custom', label: t('settings.usb.custom') }
                ]}
              />
            </SettingRow>
            <Collapse
              ghost
              activeKey={manualOpen ? ['manual'] : []}
              onChange={(keys) => setManualOpen(keys.includes('manual'))}
              items={[
                {
                  key: 'manual',
                  label: t('settings.usb.manual'),
                  children: (
                    <div className="space-y-4">
                      <Panel className="flex items-center justify-between gap-3 text-xs">
                        <span className="text-fg-muted">{t('settings.usb.budgetTitle')}</span>
                        <span className={`font-mono ${withinBudget ? 'text-fg' : 'text-warning'}`}>
                          IN {used?.in ?? '–'}/{status?.budget.inLimit ?? '–'} · OUT{' '}
                          {used?.out ?? '–'}/{status?.budget.outLimit ?? '–'}
                        </span>
                      </Panel>
                      {usbDevices.map((name) => {
                        const blocked = Boolean(
                          draft && status && !canToggleDevice(draft, name, status)
                        );
                        return (
                          <SettingRow
                            key={name}
                            label={
                              <span className={blocked ? 'text-fg-muted' : undefined}>
                                {t(`settings.usb.devices.${name}.title`)}
                              </span>
                            }
                            description={t(`settings.usb.devices.${name}.description`)}
                          >
                            <div className="flex items-center gap-2">
                              <Tag
                                bordered={false}
                                className="text-fg-muted m-0 font-mono text-[10px]"
                              >
                                {status?.costs[name].in ?? '–'} IN ·{' '}
                                {status?.costs[name].out ?? '–'} OUT
                              </Tag>
                              <Tooltip
                                title={blocked ? t('settings.usb.budgetExceeded') : undefined}
                              >
                                <span>
                                  <Switch
                                    aria-label={t(`settings.usb.devices.${name}.title`)}
                                    checked={Boolean(draft?.[name])}
                                    disabled={!status || loading || blocked}
                                    onChange={() => toggle(name)}
                                  />
                                </span>
                              </Tooltip>
                            </div>
                          </SettingRow>
                        );
                      })}
                    </div>
                  )
                }
              ]}
            />
            {draft?.absolute && (
              <SettingRow
                label={t('settings.usb.pointerProfile')}
                description={t('settings.usb.pointerProfileHelp')}
                htmlFor="usb-pointer-profile"
                stacked
              >
                <Select
                  id="usb-pointer-profile"
                  aria-describedby="usb-pointer-profile-description"
                  className="w-full"
                  value={draft.pointerProfile ?? 'default'}
                  disabled={loading || !status}
                  options={[
                    { value: 'default', label: t('settings.usb.pointerProfileDefault') },
                    {
                      value: 'windows',
                      label: 'Windows',
                      disabled: !status?.windowsPointerSupported
                    }
                  ]}
                  onChange={(value) => setDraft({ ...draft, pointerProfile: value })}
                />
              </SettingRow>
            )}
            {empty && <div className="text-fg-muted text-sm">{t('settings.usb.empty')}</div>}
            {status && !status.revision && (
              <div className="text-warning text-sm">{t('settings.usb.serverUpdateRequired')}</div>
            )}
            <div className="flex items-center gap-3">
              <Button
                type="primary"
                loading={loading}
                disabled={!dirty || !withinBudget || !status?.revision}
                onClick={apply}
              >
                {t('settings.usb.apply')}
              </Button>
              <Button
                disabled={loading || (!dirty && !error)}
                onClick={() => {
                  if (status && !error) {
                    adopt(status);
                    setError('');
                  } else {
                    void reload();
                  }
                }}
              >
                {t(error ? 'settings.usb.reload' : 'settings.usb.cancel')}
              </Button>
            </div>
            <p className="text-fg-muted m-0 text-xs">{t('settings.usb.reconnectNotice')}</p>
          </div>
          <MouseJiggler />
        </>
      )}
    </div>
  );
};
