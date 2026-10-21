import { useState } from 'react';
import type { JSX } from 'react';
import { Modal } from 'antd';
import {
  CircleDotIcon,
  HandIcon,
  HelpCircleIcon,
  MousePointerClickIcon,
  MoveIcon,
  TimerIcon
} from 'lucide-react';
import { useTranslation } from 'react-i18next';

type GuideItem = {
  key: string;
  icon: JSX.Element;
  title: string;
  desc: string;
  diagram: JSX.Element;
};

const TouchpadArea = ({ children }: { children: JSX.Element }) => {
  const { t } = useTranslation();
  return (
    <div className="relative h-[96px] overflow-hidden rounded border border-neutral-700 bg-neutral-950">
      <div className="absolute inset-3 rounded border border-dashed border-neutral-700/80" />
      <div className="absolute top-2 left-3 text-[10px] tracking-wide text-neutral-500 uppercase">
        {t('mouse.touchpadGuide.screen')}
      </div>
      {children}
    </div>
  );
};

const SwipeDiagram = () => (
  <TouchpadArea>
    <>
      <div className="absolute top-[50px] left-[30px] h-0.5 w-[74px] bg-sky-500" />
      <div className="absolute top-[44px] left-[94px] h-3 w-3 rotate-45 border-t-2 border-r-2 border-sky-500" />
      <div className="absolute top-[43px] left-[26px] h-4 w-4 rounded-full border-2 border-sky-400 bg-neutral-900" />
      <div className="absolute top-[43px] left-[100px] h-4 w-4 rounded-full bg-sky-500" />
    </>
  </TouchpadArea>
);
const TapDiagram = () => (
  <TouchpadArea>
    <>
      <div className="absolute top-1/2 left-1/2 h-9 w-9 -translate-x-1/2 -translate-y-1/2 rounded-full border border-emerald-400/60 bg-emerald-500/10" />
      <div className="absolute top-1/2 left-1/2 h-3 w-3 -translate-x-1/2 -translate-y-1/2 rounded-full bg-emerald-400" />
    </>
  </TouchpadArea>
);
const HoldDiagram = () => (
  <TouchpadArea>
    <>
      <div className="absolute top-[46px] left-1/2 h-5 w-5 -translate-x-1/2 rounded-full bg-amber-400" />
      <div className="absolute top-[41px] left-1/2 h-8 w-8 -translate-x-1/2 rounded-full border border-amber-300/70" />
      <div className="absolute bottom-3 left-1/2 -translate-x-1/2 rounded bg-neutral-800 px-2 py-0.5 text-xs text-amber-200">
        1s
      </div>
    </>
  </TouchpadArea>
);
const DragDiagram = () => (
  <TouchpadArea>
    <>
      <div className="absolute top-[50px] left-[34px] h-0.5 w-[68px] bg-blue-500" />
      <div className="absolute top-[44px] left-[92px] h-3 w-3 rotate-45 border-t-2 border-r-2 border-blue-500" />
      <div className="absolute top-[42px] left-[28px] h-5 w-5 rounded-full bg-blue-500" />
      <div className="absolute top-[38px] left-[88px] rounded bg-neutral-800 px-1.5 py-0.5 text-[10px] text-blue-200">
        L
      </div>
    </>
  </TouchpadArea>
);

export const TouchpadGuide = () => {
  const { t } = useTranslation();
  const [isModalOpen, setIsModalOpen] = useState(false);
  const items: GuideItem[] = [
    {
      key: 'swipe',
      icon: <MoveIcon size={18} />,
      title: t('mouse.touchpadGuide.swipeTitle'),
      desc: t('mouse.touchpadGuide.swipeDesc'),
      diagram: <SwipeDiagram />
    },
    {
      key: 'tap',
      icon: <MousePointerClickIcon size={18} />,
      title: t('mouse.touchpadGuide.tapTitle'),
      desc: t('mouse.touchpadGuide.tapDesc'),
      diagram: <TapDiagram />
    },
    {
      key: 'hold',
      icon: <TimerIcon size={18} />,
      title: t('mouse.touchpadGuide.holdTitle'),
      desc: t('mouse.touchpadGuide.holdDesc'),
      diagram: <HoldDiagram />
    },
    {
      key: 'drag',
      icon: <HandIcon size={18} />,
      title: t('mouse.touchpadGuide.dragTitle'),
      desc: t('mouse.touchpadGuide.dragDesc'),
      diagram: <DragDiagram />
    }
  ];

  return (
    <>
      <div
        className="flex h-[30px] cursor-pointer items-center space-x-2 rounded px-3 text-neutral-300 select-none hover:bg-neutral-700/70"
        onClick={() => setIsModalOpen(true)}
      >
        <HelpCircleIcon size={18} />
        <span>{t('mouse.touchpadGuide.title')}</span>
      </div>
      <Modal
        open={isModalOpen}
        title={t('mouse.touchpadGuide.title')}
        width={680}
        footer={false}
        onCancel={() => setIsModalOpen(false)}
      >
        <div className="space-y-4 pt-2 text-neutral-200">
          <div className="rounded border border-neutral-800 bg-neutral-950/70 px-4 py-3 text-sm text-neutral-300">
            <div className="flex items-start gap-2">
              <CircleDotIcon className="mt-0.5 shrink-0 text-sky-400" size={16} />
              <span>{t('mouse.touchpadGuide.scope')}</span>
            </div>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            {items.map((item) => (
              <div
                key={item.key}
                className="rounded border border-neutral-800 bg-neutral-900/80 p-3"
              >
                <div className="mb-2 flex items-center gap-2 text-neutral-100">
                  <div className="flex h-7 w-7 items-center justify-center rounded bg-neutral-800 text-sky-300">
                    {item.icon}
                  </div>
                  <div className="text-sm font-medium">{item.title}</div>
                </div>
                {item.diagram}
                <p className="mt-2 mb-0 text-sm leading-5 text-neutral-400">{item.desc}</p>
              </div>
            ))}
          </div>
        </div>
      </Modal>
    </>
  );
};
