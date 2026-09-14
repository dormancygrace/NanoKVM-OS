import { useRef, useState } from 'react';
import { Button, message, Tooltip } from 'antd';
import { useAtomValue } from 'jotai';
import { CameraIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { downloadScreenshot } from '@/lib/screenshot.ts';
import { isHdmiEnabledAtom, screenshotSourceAtom } from '@/jotai/screen.ts';

export const Screenshot = () => {
  const { t } = useTranslation();
  const captureEnabled = useAtomValue(isHdmiEnabledAtom);
  const source = useAtomValue(screenshotSourceAtom);
  const [saving, setSaving] = useState(false);
  const busy = useRef(false);

  const title = !captureEnabled
    ? t('screenshot.captureDisabled')
    : !source
      ? t('screenshot.noVideo')
      : saving
        ? t('screenshot.saving')
        : t('screenshot.take');

  async function takeScreenshot() {
    if (!captureEnabled || !source || busy.current) return;
    busy.current = true;
    setSaving(true);
    try {
      const blob = await source.capture();
      downloadScreenshot(blob);
      message.success(t('screenshot.saved', { width: source.width, height: source.height }));
    } catch (error) {
      message.error(
        t(
          error instanceof Error && error.message === 'screenshot-no-video'
            ? 'screenshot.noVideo'
            : 'screenshot.failed'
        )
      );
    } finally {
      busy.current = false;
      setSaving(false);
    }
  }

  return (
    <Tooltip title={title} mouseEnterDelay={0.6}>
      <span className="flex shrink-0">
        <Button
          type="text"
          aria-label={title}
          disabled={!captureEnabled || !source || saving}
          className={`flex! h-[30px]! w-[30px]! min-w-[30px]! items-center! justify-center! !p-0 [&_.ant-btn-icon]:flex! [&_.ant-btn-icon]:items-center! [&_svg]:block ${!captureEnabled || !source ? '!text-neutral-500' : '!text-neutral-300 hover:!text-white'}`}
          onClick={() => void takeScreenshot()}
          icon={<CameraIcon size={19} strokeWidth={1.7} />}
        />
      </span>
    </Tooltip>
  );
};
