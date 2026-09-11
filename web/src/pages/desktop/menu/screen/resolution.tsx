import { useState } from 'react';
import { Button, message } from 'antd';
import { useAtom } from 'jotai';
import { CheckIcon, RatioIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { updateScreen } from '@/api/vm';
import { setResolution } from '@/lib/localstorage';
import { resolutionAtom } from '@/jotai/screen';
import { MenuSubmenu } from '@/components/menu-item.tsx';

export const Resolution = () => {
  const { t } = useTranslation();
  const [resolution, setCurrent] = useAtom(resolutionAtom);
  const [busy, setBusy] = useState(false);
  const choices = [
    { width: 0, height: 0 },
    { width: 2560, height: 1440 },
    { width: 1920, height: 1080 },
    { width: 1280, height: 720 },
    { width: 800, height: 600 }
  ];
  const label = (h: number) =>
    h ? t('videoSettings.atMost', { value: `${h}p` }) : t('videoSettings.sameAsInput');
  const content = (
    <div className="max-w-72">
      <p className="mb-2 px-1 text-xs text-neutral-400">{t('videoSettings.limitHint')}</p>
      {choices.map((item) => (
        <Button
          type="text"
          key={item.height}
          disabled={busy}
          className="!flex w-full items-center gap-2 rounded px-2 py-2 text-left hover:bg-neutral-700/70 disabled:opacity-50"
          onClick={async () => {
            setBusy(true);
            try {
              const rsp = await updateScreen('resolution', item.height);
              if (rsp.code !== 0) {
                message.error(rsp.msg);
                return;
              }
              setCurrent(item);
              setResolution(item);
            } catch {
              message.error(t('videoSettings.failed'));
            } finally {
              setBusy(false);
            }
          }}
        >
          <span className="w-4 text-blue-400">
            {resolution?.height === item.height && <CheckIcon size={15} />}
          </span>
          {label(item.height)}
        </Button>
      ))}
    </div>
  );
  return (
    <MenuSubmenu
      title={t('videoSettings.streamResolution')}
      content={content}
      popoverProps={{ trigger: 'click', placement: 'rightTop', arrow: false }}
    >
      <Button
        type="text"
        className="!flex min-h-9 w-full items-center gap-2 rounded px-3 text-left text-sm text-neutral-300 hover:bg-neutral-700/70"
      >
        <RatioIcon size={18} />
        <span>{t('videoSettings.streamResolution')}</span>
        <span className="ml-auto text-xs text-neutral-400">{label(resolution?.height ?? 0)}</span>
      </Button>
    </MenuSubmenu>
  );
};
