import { useState } from 'react';
import { Button, Tooltip } from 'antd';
import { useAtomValue } from 'jotai';
import { VideoIcon, VideoOffIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { changeCapture } from '@/lib/capture-control.ts';
import { captureBusyAtom, captureReadyAtom, isHdmiEnabledAtom } from '@/jotai/screen.ts';

export const Capture = () => {
  const { t } = useTranslation();
  const enabled = useAtomValue(isHdmiEnabledAtom);
  const ready = useAtomValue(captureReadyAtom);
  const busy = useAtomValue(captureBusyAtom);
  const [error, setError] = useState(false);
  const title = t(error ? 'capture.failed' : enabled ? 'capture.stop' : 'capture.start');
  return (
    <Tooltip title={title}>
      <Button
        type="text"
        aria-label={title}
        aria-pressed={enabled}
        disabled={!ready || busy}
        className="!flex !h-[30px] !w-[30px] !min-w-0 !items-center !justify-center !rounded !p-0 hover:!bg-neutral-700/80 disabled:!opacity-40"
        style={{
          color: enabled ? '#34d399' : '#fbbf24',
          backgroundColor: 'transparent',
          border: 0,
          boxShadow: 'none'
        }}
        onClick={() => {
          setError(false);
          void changeCapture(!enabled).catch(() => setError(true));
        }}
      >
        {enabled ? (
          <VideoIcon size={22} strokeWidth={1.8} className="block shrink-0" />
        ) : (
          <VideoOffIcon size={22} strokeWidth={1.8} className="block shrink-0" />
        )}
      </Button>
    </Tooltip>
  );
};
