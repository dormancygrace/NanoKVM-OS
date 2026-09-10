import { useState } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { Button } from 'antd';
import { useAtomValue } from 'jotai';
import { VideoIcon, VideoOffIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { changeCapture } from '@/lib/capture-control.ts';
import { captureBusyAtom, captureReadyAtom } from '@/jotai/screen.ts';

export function CapturePaused() {
  const { t } = useTranslation();
  const { account } = useAuth();
  const ready = useAtomValue(captureReadyAtom);
  const busy = useAtomValue(captureBusyAtom);
  const [error, setError] = useState(false);
  return (
    <div className="absolute inset-0 flex items-center justify-center bg-black px-6 text-neutral-300">
      <div className="flex max-w-sm flex-col items-center gap-5 text-center">
        <div className="flex h-16 w-16 items-center justify-center rounded-2xl border border-neutral-800 bg-neutral-900/70 text-neutral-400">
          <VideoOffIcon size={28} strokeWidth={1.5} aria-hidden="true" />
        </div>
        <span role="status" className="text-lg font-medium tracking-tight text-neutral-200">
          {t(ready ? 'capture.off' : 'capture.checking')}
        </span>
        {ready && account.role === 'admin' && (
          <Button
            type="primary"
            size="large"
            loading={busy}
            icon={<VideoIcon size={17} />}
            className="!rounded-lg !px-5"
            onClick={() => {
              setError(false);
              void changeCapture(true).catch(() => setError(true));
            }}
          >
            {t('capture.start')}
          </Button>
        )}
        {error && (
          <span role="alert" className="text-red-400">
            {t('capture.failed')}
          </span>
        )}
      </div>
    </div>
  );
}
