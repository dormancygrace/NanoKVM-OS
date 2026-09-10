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
          backgroundColor: enabled ? 'rgba(16,185,129,0.16)' : 'rgba(245,158,11,0.12)',
          boxShadow: `inset 0 0 0 1px ${enabled ? 'rgba(52,211,153,0.3)' : 'rgba(251,191,36,0.25)'}`
        }}
        onClick={() => {
          setError(false);
          void changeCapture(!enabled).catch(() => setError(true));
        }}
      >
        {enabled ? <VideoIcon size={18} /> : <VideoOffIcon size={18} />}
      </Button>
    </Tooltip>
  );
};
