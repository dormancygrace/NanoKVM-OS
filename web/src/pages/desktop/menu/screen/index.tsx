import { Button } from 'antd';
import { useAtomValue, useSetAtom } from 'jotai';
import { ClapperboardIcon, MonitorIcon, SettingsIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getEncoderCodec } from '@/lib/encoder';
import { videoModeAtom, videoSessionCountAtom } from '@/jotai/screen';
import { menuCloseSignalAtom, settingsRequestAtom } from '@/jotai/settings';
import { useResponsiveDevice } from '@/hooks/useResponsiveDevice';
import { MenuItem } from '@/components/menu-item';
import { useDismissMobileMenu } from '@/components/mobile-menu-context';

import { QuickVideo } from './quick-video';
import { Scale } from './scale';

export const Screen = () => {
  const { t } = useTranslation();
  const mode = useAtomValue(videoModeAtom);
  const sessions = useAtomValue(videoSessionCountAtom);
  const codec = getEncoderCodec();
  const streamLabel =
    mode === 'mjpeg'
      ? 'MJPEG'
      : `${mode === 'direct' ? 'Direct' : 'WebRTC'} · ${codec === 'h265' ? 'H.265' : 'H.264'}`;
  const openSettings = useSetAtom(settingsRequestAtom);
  const closeMenu = useSetAtom(menuCloseSignalAtom);
  const dismissMobileMenu = useDismissMobileMenu();
  const { isMobilePortrait } = useResponsiveDevice();
  const content = (
    <div className="flex! min-w-64 flex-col gap-1">
      <div className="flex items-center justify-between gap-4 px-3 py-2 text-sm">
        <span className="flex items-center gap-2 text-neutral-400">
          <ClapperboardIcon size={18} />
          {t(mode === 'mjpeg' ? 'screen.video' : 'screen.codec')}
        </span>
        <span className="font-medium whitespace-nowrap text-sky-300">{streamLabel}</span>
      </div>
      <QuickVideo />
      <Scale />
      <div className="my-1 border-t border-neutral-700" />
      <Button
        type="text"
        className="flex! h-9 items-center gap-2 rounded px-3 text-sm text-neutral-300 hover:bg-neutral-700/70"
        onClick={() => {
          // Collapsing the mobile rail unmounts Settings and discards its open dialog.
          // Close only the popover while the settings request is being handled.
          if (isMobilePortrait) dismissMobileMenu();
          else closeMenu((n) => n + 1);
          openSettings('video');
        }}
      >
        <SettingsIcon size={18} />
        {t('videoSettings.open')}
      </Button>
    </div>
  );
  return (
    <MenuItem
      title={t('screen.title')}
      icon={
        <span
          className="relative inline-flex"
          role="img"
          aria-label={
            sessions === null ? t('screen.title') : t('screen.sessions', { count: sessions })
          }
        >
          <MonitorIcon size={18} />
          {sessions !== null && (
            <span
              aria-hidden="true"
              style={sessions > 0 ? { backgroundColor: '#38bdf8' } : undefined}
              className={`pointer-events-none absolute -top-1 -right-1 flex h-4 w-4 items-center justify-center rounded-full font-mono text-[11px] leading-none font-bold ring-2 ring-neutral-800 ${sessions > 99 ? 'text-[7px]!' : sessions > 9 ? 'text-[9px]!' : ''} ${sessions > 0 ? 'text-neutral-950' : 'bg-neutral-600 text-neutral-200'}`}
            >
              {sessions > 99 ? '99+' : sessions}
            </span>
          )}
        </span>
      }
      content={content}
    />
  );
};
