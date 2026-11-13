import { useTranslation } from 'react-i18next';

import { LedIndicator, LedPanel, type LedState } from '../led-indicator';
import { useKeyboardLedStatus } from './use-keyboard-led-status';

const LOCKS = [
  { key: 'numLock', symbol: '1' },
  { key: 'capsLock', symbol: 'A' },
  { key: 'scrollLock', symbol: '⤓' }
] as const;

export function KeyboardLedStatus() {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.keyboardLedStatus' });
  const status = useKeyboardLedStatus();
  const disabled = status?.keyboardEnabled === false;
  const known = !disabled && (status?.known ?? false);

  return (
    <LedPanel label={t('groupLabel')}>
      {LOCKS.map(({ key, symbol }) => {
        const state: LedState = disabled
          ? 'disabled'
          : known
            ? status?.[key]
              ? 'on'
              : 'off'
            : 'unknown';
        const stateLabel = t(state === 'disabled' ? 'keyboardDisabled' : state);

        return (
          <LedIndicator
            key={key}
            description={t('indicatorLabel', { label: t(key), state: stateLabel })}
            shortLabel={t(`${key}Short`)}
            symbol={symbol}
            state={state}
          />
        );
      })}
    </LedPanel>
  );
}
