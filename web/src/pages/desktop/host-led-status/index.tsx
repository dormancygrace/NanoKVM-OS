import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { subscribeLEDStatus } from '@/lib/led-status.ts';

import { LedIndicator, LedPanel, type LedState } from '../led-indicator';

type HostLeds = { pwr: boolean; hdd: boolean } | null;

// The power and disk LEDs of the host's front panel, as the ATX header
// reports them: green for power, a soft red for disk activity.
export function HostLedStatus() {
  const { t } = useTranslation('translation', { keyPrefix: 'settings' });
  const [leds, setLeds] = useState<HostLeds>(null);

  useEffect(() => subscribeLEDStatus(setLeds), []);

  const state = (lit: boolean | undefined): LedState =>
    leds === null ? 'unknown' : lit ? 'on' : 'off';
  const describe = (label: string, value: LedState) =>
    t('keyboardLedStatus.indicatorLabel', { label, state: t(`keyboardLedStatus.${value}`) });

  return (
    <LedPanel label={t('hostLedStatus.groupLabel')}>
      <LedIndicator
        description={describe(t('hostLedStatus.powerLed'), state(leds?.pwr))}
        shortLabel={t('hostLedStatus.powerLedShort')}
        symbol="P"
        state={state(leds?.pwr)}
        onClassName="bg-green-500"
      />
      <LedIndicator
        description={describe(t('hostLedStatus.hddLed'), state(leds?.hdd))}
        shortLabel={t('hostLedStatus.hddLedShort')}
        symbol="H"
        state={state(leds?.hdd)}
        onClassName="bg-red-400"
      />
    </LedPanel>
  );
}
