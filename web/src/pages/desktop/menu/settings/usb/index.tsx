import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Collapse, Divider, Select, Switch, Tag, Tooltip } from 'antd';
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

export const Usb = () => {
  const { t } = useTranslation();
  const [status, setStatus] = useState<UsbStatus>();
  const [draft, setDraft] = useState<UsbComposition>();
  const [selection, setSelection] = useState('custom');
  const [manualOpen, setManualOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const busy = useRef(false);
  const [error, setError] = useState('');

  const adopt = useCallback((next: UsbStatus) => {
    setStatus(next);
    setDraft(next);
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
        if (active) setError(t('settings.usb.loadFailed'));
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
        setDraft({ ...preset.composition });
    }
    setError('');
  }

  function toggle(name: UsbDevice) {
    if (!status || !draft || busy.current || !canToggleDevice(draft, name, status)) return;
    setDraft(toggleDevice(draft, name));
    setSelection('custom');
    setError('');
  }

  async function apply() {
    if (
      !status?.revision ||
      !draft ||
      busy.current ||
      !fitsBudget(draft, status) ||
      !usbDevices.some((name) => draft[name])
    )
      return;
    busy.current = true;
    setLoading(true);
    setError('');
    const requested = draft;
    try {
      const rsp = await api.setUsbComposition(requested, status.revision);
      if (rsp.code === 0) {
        adopt(normalizeUsbStatus(rsp.data));
      } else {
        adopt(await fetchStatus());
        setError(t(rsp.code === -5 ? 'settings.usb.changed' : 'settings.usb.updateFailed'));
      }
    } catch {
      // A USB network disconnect can lose a successful response. Read back;
      // never blindly replay a mutation after an uncertain result.
      try {
        const next = await fetchStatus();
        adopt(next);
        if (!sameComposition(next, requested)) setError(t('settings.usb.updateFailed'));
      } catch {
        setError(t('settings.usb.reconnectFailed'));
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
    }
  }

  const used = draft && status ? endpointUsage(draft, status.costs) : undefined;
  const dirty = Boolean(draft && status && !sameComposition(draft, status));
  const empty = Boolean(draft && !usbDevices.some((name) => draft[name]));
  const withinBudget = Boolean(draft && status && fitsBudget(draft, status));

  return (
    <>
      <div className="text-base">{t('settings.usb.title')}</div>
      <Divider className="opacity-50" />
      <label htmlFor="usb-preset" className="mb-2 block text-sm text-neutral-400">
        {t('settings.usb.presetLabel')}
      </label>
      <Select
        id="usb-preset"
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
      <p className="mb-4 mt-2 text-xs text-neutral-400">
        {selection === 'custom'
          ? t('settings.usb.customDescription')
          : t(`settings.usb.presets.${selection}.description`)}
      </p>
      <Collapse
        ghost
        activeKey={manualOpen ? ['manual'] : []}
        onChange={(keys) => setManualOpen(keys.includes('manual'))}
        items={[
          {
            key: 'manual',
            label: t('settings.usb.manual'),
            children: (
              <div className="flex flex-col gap-5">
                <div className="flex items-center justify-between rounded-lg bg-neutral-800/60 p-3 text-xs">
                  <span className="text-neutral-400">{t('settings.usb.budgetTitle')}</span>
                  <span
                    className={`font-mono ${withinBudget ? 'text-neutral-300' : 'text-amber-400'}`}
                  >
                    IN {used?.in ?? '–'}/{status?.budget.inLimit ?? '–'} · OUT {used?.out ?? '–'}/
                    {status?.budget.outLimit ?? '–'}
                  </span>
                </div>
                {usbDevices.map((name) => {
                  const blocked = Boolean(draft && status && !canToggleDevice(draft, name, status));
                  return (
                    <div key={name} className="flex items-center justify-between gap-3">
                      <div className="min-w-0">
                        <div className={blocked ? 'text-neutral-500' : ''}>
                          {t(`settings.usb.devices.${name}.title`)}
                        </div>
                        <div className="text-xs text-neutral-500">
                          {t(`settings.usb.devices.${name}.description`)}
                        </div>
                      </div>
                      <div className="flex shrink-0 items-center gap-2">
                        <Tag
                          bordered={false}
                          className="m-0 font-mono text-[10px] text-neutral-400"
                        >
                          {status?.costs[name].in ?? '–'} IN · {status?.costs[name].out ?? '–'} OUT
                        </Tag>
                        <Tooltip title={blocked ? t('settings.usb.budgetExceeded') : undefined}>
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
                    </div>
                  );
                })}
              </div>
            )
          }
        ]}
      />
      {empty && <div className="mt-3 text-sm text-amber-400">{t('settings.usb.empty')}</div>}
      {status && !status.revision && (
        <div className="mt-3 text-sm text-amber-400">{t('settings.usb.serverUpdateRequired')}</div>
      )}
      {error && (
        <div role="alert" className="mt-3 text-sm text-red-400">
          {error}
        </div>
      )}
      <div className="mt-5 flex items-center gap-3">
        <Button
          type="primary"
          loading={loading}
          disabled={!dirty || empty || !withinBudget || !status?.revision}
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
      <p className="mt-3 text-xs text-neutral-500">{t('settings.usb.reconnectNotice')}</p>
    </>
  );
};
