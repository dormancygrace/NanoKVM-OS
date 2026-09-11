import { useState } from 'react';
import { CheckOutlined, CloseOutlined } from '@ant-design/icons';
import { Button, InputNumber } from 'antd';
import { CheckIcon, ScanBarcodeIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { updateScreen } from '@/api/vm';
import { setFps as setCookie } from '@/lib/localstorage';
import { MenuSubmenu } from '@/components/menu-item.tsx';

const fpsList = [
  { key: 60, label: '60 FPS' },
  { key: 30, label: '30 FPS' },
  { key: 15, label: '15 FPS' },
  { key: 10, label: '10 FPS' }
];

const defaultFps = fpsList.map((item) => item.key);

type FpsProps = {
  fps: number;
  setFps: (fps: number) => void;
  maxFps?: number;
};

export const Fps = ({ fps, setFps, maxFps = 60 }: FpsProps) => {
  const { t } = useTranslation();
  const [isCustomize, setIsCustomize] = useState(false);
  const [customFps, setCustomFps] = useState<number | null>(fps);
  const customValid =
    customFps !== null && Number.isInteger(customFps) && customFps >= 10 && customFps <= maxFps;

  function showCustomize() {
    setCustomFps(fps);
    setIsCustomize(true);
  }

  async function update(value: number) {
    if (!Number.isInteger(value) || value < 10 || value > maxFps) return;
    if (isCustomize && value === fps) {
      setIsCustomize(false);
      return;
    }

    const rsp = await updateScreen('fps', value);
    if (rsp.code !== 0) {
      return;
    }

    const effective = rsp.data?.fps ?? value;
    setFps(effective);
    setCookie(effective);
    if (isCustomize) {
      setIsCustomize(false);
    }
  }

  const content = (
    <>
      {/* default fps list */}
      {fpsList
        .filter((item) => item.key <= maxFps)
        .map((item) => (
          <div
            key={item.key}
            className="flex cursor-pointer select-none items-center rounded py-1.5 pl-1 hover:bg-neutral-700/70"
            onClick={() => update(item.key)}
          >
            <div className="flex h-[14px] w-[20px] items-end text-blue-500">
              {item.key === fps && <CheckIcon size={14} />}
            </div>
            <span>{item.label}</span>
          </div>
        ))}

      {/* customize fps */}
      <div
        className="flex cursor-pointer select-none items-center rounded py-1.5 pl-1 pr-5 hover:bg-neutral-700/70"
        onClick={showCustomize}
      >
        {defaultFps.includes(fps) ? (
          <>
            <div className="flex h-[14px] w-[20px] items-end"></div>
            <span>{t('screen.customizeFps')}</span>
          </>
        ) : (
          <>
            <div className="flex h-[14px] w-[20px] items-end text-blue-500">
              <CheckIcon size={14} />
            </div>
            <span>{t('screen.customizeFps')}</span>
            <span className="text-xs">{`(${fps} FPS)`}</span>
          </>
        )}
      </div>

      {isCustomize && (
        <div className="flex w-[140px] items-center space-x-1 py-1">
          <InputNumber<number>
            value={customFps}
            min={10}
            max={maxFps}
            precision={0}
            onChange={setCustomFps}
          />
          <Button
            size="small"
            icon={<CheckOutlined />}
            disabled={!customValid}
            onClick={() => customFps !== null && update(customFps)}
          />
          <Button size="small" icon={<CloseOutlined />} onClick={() => setIsCustomize(false)} />
        </div>
      )}
    </>
  );

  return (
    <MenuSubmenu
      title={t('screen.fps')}
      content={content}
      popoverProps={{ placement: 'rightTop', arrow: false, align: { offset: [14, 0] } }}
      onBeforeLeave={() => !isCustomize}
    >
      <div className="flex h-[30px] cursor-pointer items-center space-x-2 rounded px-3 text-neutral-300 hover:bg-neutral-700/70">
        <ScanBarcodeIcon size={18} />
        <span className="select-none text-sm">{t('screen.fps')}</span>
        <span className="ml-auto text-xs text-neutral-400">{fps} FPS</span>
      </div>
    </MenuSubmenu>
  );
};
