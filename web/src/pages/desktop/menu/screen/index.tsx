import { useEffect } from 'react';
import { useAuth } from '@/contexts/auth';
import { Button } from 'antd';
import { useAtomValue, useSetAtom } from 'jotai';
import { MonitorIcon, SettingsIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { updateScreen } from '@/api/vm';
import { videoModeAtom } from '@/jotai/screen';
import { menuCloseSignalAtom, settingsRequestAtom } from '@/jotai/settings';
import { MenuItem } from '@/components/menu-item';

import { getScreenType } from './constants';
import { StreamControls } from './controls';
import { Scale } from './scale';

export const Screen = () => {
  const { t } = useTranslation();
  const { account } = useAuth();
  const mode = useAtomValue(videoModeAtom);
  const openSettings = useSetAtom(settingsRequestAtom);
  const closeMenu = useSetAtom(menuCloseSignalAtom);
  useEffect(() => {
    const type = getScreenType(mode);
    if (type !== null && account.role === 'admin') void updateScreen('type', type);
  }, [mode, account.role]);
  const content = (
    <div className="!flex min-w-64 flex-col gap-1">
      <StreamControls />
      <Scale />
      <div className="my-1 border-t border-neutral-700" />
      <Button
        type="text"
        className="!flex h-9 items-center gap-2 rounded px-3 text-sm text-neutral-300 hover:bg-neutral-700/70"
        onClick={() => {
          closeMenu((n) => n + 1);
          openSettings('video');
        }}
      >
        <SettingsIcon size={18} />
        {t('videoSettings.open')}
      </Button>
    </div>
  );
  return <MenuItem title={t('screen.title')} icon={<MonitorIcon size={18} />} content={content} />;
};
