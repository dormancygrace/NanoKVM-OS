import { useEffect } from 'react';
import { Button, Tooltip } from 'antd';
import { useAtom, useAtomValue, useSetAtom } from 'jotai';
import { ScanTextIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getAddonInventory } from '@/api/addons';
import { addonInventoryAtom } from '@/jotai/addons.ts';
import { isHdmiEnabledAtom, screenTextOpenAtom } from '@/jotai/screen.ts';

// Reads the text on the screen with the TPU add-on; shown once the add-on is
// installed, unless text recognition is off (Settings > AI).
export const ScreenText = () => {
  const { t } = useTranslation();
  const captureEnabled = useAtomValue(isHdmiEnabledAtom);
  const [inventory, setInventory] = useAtom(addonInventoryAtom);
  const setOpen = useSetAtom(screenTextOpenAtom);

  useEffect(() => {
    if (inventory) return;
    let active = true;
    getAddonInventory()
      .then((response) => {
        if (active && response.code === 0) setInventory(response.data);
      })
      .catch(() => {
        /* Without the inventory the button stays hidden. */
      });
    return () => {
      active = false;
    };
  }, [inventory, setInventory]);

  if (!inventory?.tpu?.installed || inventory.tpu.enabled === false) return null;

  const title = captureEnabled ? t('screenText.read') : t('screenText.captureDisabled');
  return (
    <Tooltip title={title} mouseEnterDelay={0.6}>
      <span className="flex shrink-0">
        <Button
          type="text"
          aria-label={title}
          disabled={!captureEnabled}
          className={`flex! h-[30px]! w-[30px]! min-w-[30px]! items-center! justify-center! !p-0 [&_.ant-btn-icon]:flex! [&_.ant-btn-icon]:items-center! [&_svg]:block ${captureEnabled ? '!text-neutral-300 hover:!text-white' : '!text-neutral-500'}`}
          onClick={() => setOpen(true)}
          icon={<ScanTextIcon size={19} strokeWidth={1.7} />}
        />
      </span>
    </Tooltip>
  );
};
