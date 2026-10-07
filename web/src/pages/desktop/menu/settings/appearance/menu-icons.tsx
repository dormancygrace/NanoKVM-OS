import { useAuth } from '@/contexts/auth.ts';
import { Switch } from 'antd';
import { useAtom } from 'jotai';
import {
  DiscIcon,
  DownloadIcon,
  FileJsonIcon,
  KeyboardIcon,
  LogOutIcon,
  MaximizeIcon,
  MouseIcon,
  NetworkIcon,
  PowerIcon,
  TerminalSquareIcon,
  VideoIcon,
  XIcon
} from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as ls from '@/lib/localstorage.ts';
import { menuDisabledItemsAtom } from '@/jotai/settings.ts';
import { Robot } from '@/components/icons/robot.tsx';
import { Panel, SettingRow } from '@/components/ui/settings.tsx';

export const MenuIcons = () => {
  const { t } = useTranslation();
  const { account } = useAuth();

  const [menuDisabledItems, setMenuDisabledItems] = useAtom(menuDisabledItemsAtom);

  const items = [
    { key: 'keyboard', icon: <KeyboardIcon size={16} /> },
    { key: 'mouse', icon: <MouseIcon size={16} /> },
    { key: 'image', icon: <DiscIcon size={16} /> },
    { key: 'download', icon: <DownloadIcon size={16} /> },
    { key: 'terminal', icon: <TerminalSquareIcon size={16} /> },
    { key: 'script', icon: <FileJsonIcon size={16} /> },
    { key: 'wol', icon: <NetworkIcon size={16} /> },
    { key: 'picoclaw', icon: <Robot size={16} /> },
    { key: 'recorder', icon: <VideoIcon size={16} /> },
    { key: 'power', icon: <PowerIcon size={16} /> },
    { key: 'logout', icon: <LogOutIcon size={16} />, label: 'settings.account.logoutBtn' },
    { key: 'fullscreen', icon: <MaximizeIcon size={16} />, label: 'fullscreen.toggle' },
    { key: 'collapse', icon: <XIcon size={16} />, label: 'menu.collapse' }
  ].filter(
    (item) =>
      account.role === 'admin' ||
      !['image', 'download', 'terminal', 'script', 'picoclaw'].includes(item.key)
  );

  function updateItems(key: string) {
    const exist = menuDisabledItems.includes(key);

    const newItems = exist
      ? menuDisabledItems.filter((item) => item !== key)
      : [...menuDisabledItems, key];

    setMenuDisabledItems(newItems);
    ls.setMenuDisabledItems(newItems);
  }

  return (
    <SettingRow
      label={t('settings.appearance.menuBar.icons')}
      description={t('settings.appearance.menuBar.iconsDesc')}
      stacked
    >
      <Panel flush>
        <ul className="divide-line m-0 list-none divide-y p-0">
          {items.map((item) => (
            <li key={item.key} className="flex items-center justify-between gap-3 px-4 py-2.5">
              <div className="text-fg-muted flex min-w-0 items-center gap-2">
                {item.icon}
                <span className="text-fg">
                  {item.label ? t(item.label) : t(`${item.key}.title`)}
                </span>
              </div>

              <Switch
                aria-label={item.label ? t(item.label) : t(`${item.key}.title`)}
                value={!menuDisabledItems.includes(item.key)}
                onChange={() => updateItems(item.key)}
              />
            </li>
          ))}
        </ul>
      </Panel>
    </SettingRow>
  );
};
