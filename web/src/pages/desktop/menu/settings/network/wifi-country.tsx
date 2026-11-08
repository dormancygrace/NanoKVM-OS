import { useCallback, useEffect, useMemo, useState } from 'react';
import { Alert, Select } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/network.ts';
import {
  networkTrialDone,
  startNetworkTrial,
  type NetworkTrialResult
} from '@/lib/network-trial.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

// ISO 3166-1 alpha-2. Only those the kernel's regulatory database has are
// offered (supported), as no other comes into force. Their names come from
// the browser in the page's language.
const countryCodes = `
  AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ
  BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM
  DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS
  GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN
  KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ
  MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM
  PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV
  SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI
  VN VU WF WS YE YT ZA ZM ZW
`
  .trim()
  .split(/\s+/);

function regionNames(language: string) {
  try {
    return new Intl.DisplayNames([language, 'en'], { type: 'region' });
  } catch {
    return undefined;
  }
}

// The regulatory country of the radio, shared by joining networks and the
// access point. Saving restarts Wi-Fi (S30wifi), so it asks first.
export const WifiCountry = ({
  state,
  locked,
  onRestarting
}: {
  state: api.WifiStatus;
  locked: boolean;
  // Wi-Fi restarts with the new country: the page follows it as a pending
  // operation, which reports a failure.
  onRestarting?: () => void;
}) => {
  const { t, i18n } = useTranslation();
  const tr = (key: string, options?: Record<string, unknown>) =>
    t(`settings.network.wifi.${key}`, options);
  const [value, setValue] = useState<api.WifiCountry>();
  const [ap, setAP] = useState<api.WifiAPStatus>();
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    // Either may fail alone; the status poll reports a dead device.
    const [country, accessPoint] = await Promise.allSettled([
      api.getWifiCountry(),
      api.getWifiAP()
    ]);
    if (country.status === 'fulfilled' && country.value.code === 0) setValue(country.value.data);
    if (accessPoint.status === 'fulfilled' && accessPoint.value.code === 0) {
      setAP(accessPoint.value.data);
    }
  }, []);
  // Again once a restart (this one, or switching Wi-Fi on) is over.
  useEffect(() => {
    void load();
  }, [load, state.enabled, state.accessPoint, state.busy]);

  // A trial that was not kept brought the previous country back: show it.
  useEffect(() => {
    const done = (event: Event) => {
      if ((event as CustomEvent<NetworkTrialResult>).detail !== 'kept') void load();
    };
    window.addEventListener(networkTrialDone, done);
    return () => window.removeEventListener(networkTrialDone, done);
  }, [load]);

  const names = useMemo(() => regionNames(i18n.language), [i18n.language]);
  const label = useCallback(
    (code: string) => {
      if (code === '00') return t('settings.network.wifi.countryWorld');
      let name: string | undefined;
      try {
        name = names?.of(code);
      } catch {
        name = undefined;
      }
      return name && name !== code ? `${name} (${code})` : code;
    },
    [names, t]
  );
  const supported = value?.supported;
  const saved = value?.country;
  const options = useMemo(() => {
    const codes = supported
      ? countryCodes.filter((code) => supported.includes(code))
      : [...countryCodes];
    // A saved country the database lacks still shows by its name.
    if (saved && saved !== '00' && !codes.includes(saved)) codes.push(saved);
    return [
      { value: '00', label: label('00') },
      ...codes
        .map((code) => ({ value: code, label: label(code) }))
        .sort((a, b) => a.label.localeCompare(b.label, i18n.language))
    ];
  }, [label, i18n.language, supported, saved]);

  async function change(country: string) {
    if (!value || country === value.country || saving || locked) return;
    // Off, Wi-Fi only takes it at its next start.
    const restarts = state.enabled;
    if (
      restarts &&
      !(await confirmAction({
        title: tr('countryConfirm', { country: label(country) }),
        content: (
          <div className="space-y-2">
            <p>{tr('countryRestart')}</p>
            {state.thisConnection && (
              <Alert type="warning" showIcon message={tr('countryOverWifi')} />
            )}
          </div>
        ),
        okText: tr('confirmBtn'),
        danger: !!state.thisConnection
      }))
    )
      return;
    setSaving(true);
    try {
      const rsp = await api.setWifiCountry(country);
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.network.wifi.operationFailed');
        return;
      }
      const { trial, ...reply } = rsp.data;
      setValue((current) => ({ ...reply, supported: current?.supported }));
      // Over Wi-Fi the country runs on trial, kept once the page reaches the
      // device again.
      if (trial) startNetworkTrial(trial);
      else if (restarts) onRestarting?.();
    } catch (err) {
      showRequestError(err, 'settings.network.wifi.operationFailed');
    } finally {
      setSaving(false);
    }
  }

  // The domain in force, when it is not the saved one: the database lacks
  // the country, or Wi-Fi has not restarted with it yet.
  const active = value?.active && value.active !== value.country ? value.active : '';
  // A 5 GHz access point runs on 2.4 GHz until a country allows it: on its
  // fallback, or with no country while its saved band is 5 GHz.
  const fallback =
    ap?.bandFallback ?? (value?.country === '00' && ap?.band === '5' ? 'country_unset' : undefined);
  return (
    <>
      {fallback && (
        <Alert
          type="warning"
          showIcon
          message={tr('countryNeededTitle')}
          description={tr(fallback === 'country_unset' ? 'countryNeeded' : 'countryNoChannels')}
        />
      )}
      <SettingRow
        label={tr('country')}
        description={
          <div className="space-y-1">
            <div>{tr('countryHint')}</div>
            {active && <div>{tr('countryActive', { country: label(active) })}</div>}
          </div>
        }
        htmlFor="wifi-country"
      >
        <Select
          id="wifi-country"
          aria-describedby="wifi-country-description"
          style={{ width: 240 }}
          showSearch={{ optionFilterProp: 'label' }}
          loading={!value || saving}
          value={value?.country}
          disabled={!value || saving || locked}
          options={options}
          onChange={(country: string) => void change(country)}
        />
      </SettingRow>
    </>
  );
};
