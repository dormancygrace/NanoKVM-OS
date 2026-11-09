import { useEffect, useRef, useState } from 'react';
import { Button, Input, message, Select, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import {
  blankIdentity,
  identityFromEDID,
  identityFromKey,
  identityIssue,
  identityKey,
  productHex,
  type MonitorIdentity
} from '@/lib/monitor-identity';

type Props = {
  // The draft identity (identityKey): '' is NanoKVM's own.
  value: string;
  onChange: (value: string) => void;
  disabled: boolean;
  row: (label: string, control: React.ReactNode, note?: React.ReactNode) => React.ReactNode;
};

// The numeric fields as typed, so a half-typed value is not rewritten.
type NumberText = { productCode: string; serialNumber: string; week: string; year: string };

const numberText = (id: MonitorIdentity | null): NumberText => ({
  productCode: id && Number.isFinite(id.productCode) ? productHex(id.productCode) : '',
  serialNumber:
    id && id.serialNumber !== null && Number.isFinite(id.serialNumber) && id.serialNumber >= 0
      ? String(id.serialNumber)
      : '',
  week: id && Number.isFinite(id.week) ? String(id.week) : '',
  year: id && Number.isFinite(id.year) ? String(id.year) : ''
});

const parseHex = (s: string) => (/^[0-9a-fA-F]{1,4}$/.test(s) ? parseInt(s, 16) : NaN);
const parseDecimal = (s: string) => (/^\d{1,10}$/.test(s) ? Number(s) : NaN);

/*
 * The identity the monitor profiles carry: manufacturer, product code, name
 * and serial number. The resolutions and refresh rates stay NanoKVM's own.
 * Part of the video draft: it is written with the profile, once, by Apply.
 */
export const MonitorIdentityFields = ({ value, onChange, disabled, row }: Props) => {
  const { t } = useTranslation();
  const identity = identityFromKey(value);
  const [text, setText] = useState<NumberText>(() => numberText(identity));
  const file = useRef<HTMLInputElement>(null);

  // Follow the draft when it changes elsewhere (Discard, a file, the device).
  useEffect(() => {
    const current = identityFromKey(value);
    setText((typed) => {
      if (!current) return numberText(null);
      const fromTyped = identityKey({
        ...current,
        productCode: parseHex(typed.productCode),
        serialNumber: current.serialNumber === null ? null : parseDecimal(typed.serialNumber),
        week: parseDecimal(typed.week),
        year: parseDecimal(typed.year)
      });
      return fromTyped === value ? typed : numberText(current);
    });
  }, [value]);

  const set = (next: MonitorIdentity, typed = text) => {
    setText(typed);
    onChange(identityKey(next));
  };
  const setNumber = (field: keyof NumberText, typed: string) => {
    if (!identity) return;
    const nextText = { ...text, [field]: typed };
    const parsed = field === 'productCode' ? parseHex(typed) : parseDecimal(typed);
    set({ ...identity, [field]: parsed }, nextText);
  };

  async function load(f: File) {
    const result = identityFromEDID(new Uint8Array(await f.arrayBuffer()));
    if (!result.ok) {
      message.error(t(`videoSettings.identity.fileError.${result.error}`));
      return;
    }
    set(result.identity, numberText(result.identity));
    message.success(t('videoSettings.identity.fileLoaded'));
  }

  const issue = identity ? identityIssue(identity) : undefined;
  const status = (field: string) => (issue === field ? 'error' : undefined);
  const fixedSerial = identity?.serialNumber !== null;

  return (
    <div className="space-y-3">
      <p className="text-fg-muted m-0 text-xs leading-relaxed">
        {t('videoSettings.identity.hint')}
      </p>
      {row(
        t('videoSettings.identity.custom'),
        <Switch
          className="self-start"
          aria-label={t('videoSettings.identity.custom')}
          checked={!!identity}
          disabled={disabled}
          onChange={(on) => {
            if (on) {
              const blank = blankIdentity();
              set(blank, numberText(blank));
            } else {
              setText(numberText(null));
              onChange('');
            }
          }}
        />,
        identity ? undefined : t('videoSettings.identity.nanokvm')
      )}
      {identity && (
        <>
          {row(
            t('videoSettings.identity.file'),
            <>
              <Button
                className="self-start"
                disabled={disabled}
                onClick={() => file.current?.click()}
              >
                {t('videoSettings.identity.fileLoad')}
              </Button>
              <input
                ref={file}
                type="file"
                className="hidden"
                accept=".bin,.edid,.dat,.txt,.raw"
                onChange={(e) => {
                  const f = e.target.files?.[0];
                  e.target.value = '';
                  if (f) void load(f);
                }}
              />
            </>,
            t('videoSettings.identity.fileHint')
          )}
          {row(
            t('videoSettings.identity.manufacturer'),
            <Input
              aria-label={t('videoSettings.identity.manufacturer')}
              value={identity.manufacturer}
              maxLength={3}
              status={status('manufacturer')}
              disabled={disabled}
              onChange={(e) => set({ ...identity, manufacturer: e.target.value.toUpperCase() })}
            />,
            t('videoSettings.identity.manufacturerHint')
          )}
          {row(
            t('videoSettings.identity.productCode'),
            <Input
              aria-label={t('videoSettings.identity.productCode')}
              value={text.productCode}
              maxLength={4}
              prefix="0x"
              status={status('productCode')}
              disabled={disabled}
              onChange={(e) => setNumber('productCode', e.target.value.trim())}
            />,
            t('videoSettings.identity.productCodeHint')
          )}
          {row(
            t('videoSettings.identity.name'),
            <Input
              aria-label={t('videoSettings.identity.name')}
              value={identity.name}
              maxLength={13}
              status={status('name')}
              disabled={disabled}
              onChange={(e) => set({ ...identity, name: e.target.value })}
            />,
            t('videoSettings.identity.nameHint')
          )}
          {row(
            t('videoSettings.identity.serial'),
            <Select
              className="w-full"
              aria-label={t('videoSettings.identity.serial')}
              value={fixedSerial ? 'fixed' : 'profile'}
              disabled={disabled}
              onChange={(mode: string) => {
                if (mode === 'profile') {
                  set(
                    { ...identity, serialNumber: null, serialText: '' },
                    { ...text, serialNumber: '' }
                  );
                } else {
                  set({ ...identity, serialNumber: parseDecimal(text.serialNumber) });
                }
              }}
              options={[
                { value: 'profile', label: t('videoSettings.identity.serialProfile') },
                { value: 'fixed', label: t('videoSettings.identity.serialFixed') }
              ]}
            />,
            t(
              fixedSerial
                ? 'videoSettings.identity.serialFixedHint'
                : 'videoSettings.identity.serialProfileHint'
            )
          )}
          {fixedSerial &&
            row(
              t('videoSettings.identity.serialNumber'),
              <Input
                aria-label={t('videoSettings.identity.serialNumber')}
                value={text.serialNumber}
                inputMode="numeric"
                status={status('serialNumber')}
                disabled={disabled}
                onChange={(e) => setNumber('serialNumber', e.target.value.trim())}
              />
            )}
          {fixedSerial &&
            row(
              t('videoSettings.identity.serialText'),
              <Input
                aria-label={t('videoSettings.identity.serialText')}
                value={identity.serialText}
                maxLength={13}
                status={status('serialText')}
                disabled={disabled}
                onChange={(e) => set({ ...identity, serialText: e.target.value })}
              />,
              t('videoSettings.identity.serialTextHint')
            )}
          {row(
            t('videoSettings.identity.manufactured'),
            <div className="flex gap-2">
              <Input
                aria-label={t('videoSettings.identity.week')}
                addonBefore={t('videoSettings.identity.week')}
                value={text.week}
                inputMode="numeric"
                status={status('week')}
                disabled={disabled}
                onChange={(e) => setNumber('week', e.target.value.trim())}
              />
              <Input
                aria-label={t('videoSettings.identity.year')}
                addonBefore={t('videoSettings.identity.year')}
                value={text.year}
                inputMode="numeric"
                status={status('year')}
                disabled={disabled}
                onChange={(e) => setNumber('year', e.target.value.trim())}
              />
            </div>,
            t('videoSettings.identity.manufacturedHint')
          )}
          {issue && (
            <p className="text-danger m-0 text-xs" role="alert">
              {t(`videoSettings.identity.invalid.${issue}`)}
            </p>
          )}
        </>
      )}
    </div>
  );
};
