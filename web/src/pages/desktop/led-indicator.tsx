import type { ReactNode } from 'react';
import { Tooltip } from 'antd';
import clsx from 'clsx';

// The small status panels beside the menu bar: the keyboard locks and the
// host's power and disk LEDs. A row is a dot and, from the sm width up, a
// short label; below it the dot carries a one-character symbol instead.
export type LedState = 'on' | 'off' | 'unknown' | 'disabled';

type LedIndicatorProps = {
  // The whole sentence for the tooltip and assistive technology.
  description: string;
  shortLabel: string;
  symbol: string;
  state: LedState;
  // The lit dot and the symbol on it.
  onClassName?: string;
  onSymbolClassName?: string;
};

export function LedIndicator({
  description,
  shortLabel,
  symbol,
  state,
  onClassName = 'bg-emerald-400',
  onSymbolClassName = 'text-neutral-900'
}: LedIndicatorProps) {
  const known = state === 'on' || state === 'off';

  return (
    <Tooltip title={description} placement="bottom" mouseEnterDelay={0.6}>
      <div
        className="flex h-[8px] items-center gap-1 px-1 text-[8px] leading-[8px] font-medium text-neutral-400"
        aria-label={description}
        role="img"
      >
        <span
          aria-hidden="true"
          className={clsx(
            'flex h-2 w-2 shrink-0 items-center justify-center rounded-full text-[7px] leading-none',
            known
              ? state === 'on'
                ? onClassName
                : 'bg-neutral-600'
              : 'border border-dashed border-neutral-500 text-neutral-300'
          )}
        >
          {known ? (
            <span
              className={clsx('sm:hidden', state === 'on' ? onSymbolClassName : 'text-neutral-100')}
            >
              {symbol}
            </span>
          ) : state === 'disabled' ? (
            '–'
          ) : (
            '?'
          )}
        </span>
        <span className="hidden sm:inline">{shortLabel}</span>
      </div>
    </Tooltip>
  );
}

export function LedPanel({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div
      className="flex h-full w-4 shrink-0 flex-col items-start justify-center gap-0.5 rounded bg-neutral-800/80 font-mono sm:w-10"
      aria-label={label}
      role="group"
    >
      {children}
    </div>
  );
}
