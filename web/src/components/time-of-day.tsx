import { Select } from 'antd';
import { useTranslation } from 'react-i18next';

import { useDeviceTime } from '@/hooks/useDeviceTime.ts';

type TimeOfDayProps = {
  hour: number;
  minute: number;
  onChange: (hour: number, minute: number) => void;
  disabled?: boolean;
  id?: string;
  hourLabel: string;
  minuteLabel: string;
};

// A time of day as an hour list, labelled in the device's 12/24-hour format
// (System > Date and time), and a minute list. A native time input would
// show the browser's format instead.
export const TimeOfDay = ({
  hour,
  minute,
  onChange,
  disabled,
  id,
  hourLabel,
  minuteLabel
}: TimeOfDayProps) => {
  const { i18n } = useTranslation();
  const preferences = useDeviceTime();
  const hours = new Intl.DateTimeFormat(i18n.language, {
    timeZone: 'UTC',
    hourCycle: preferences.format === '12' ? 'h12' : 'h23',
    hour: '2-digit'
  });
  return (
    <div className="flex items-center gap-2">
      <Select
        id={id}
        className="w-28"
        value={hour}
        disabled={disabled}
        aria-label={hourLabel}
        onChange={(value: number) => onChange(value, minute)}
        options={Array.from({ length: 24 }, (_, value) => ({
          value,
          label: hours.format(Date.UTC(2026, 0, 1, value))
        }))}
      />
      <span>:</span>
      <Select
        className="w-20"
        value={minute}
        disabled={disabled}
        aria-label={minuteLabel}
        onChange={(value: number) => onChange(hour, value)}
        options={Array.from({ length: 60 }, (_, value) => ({
          value,
          label: String(value).padStart(2, '0')
        }))}
      />
    </div>
  );
};
