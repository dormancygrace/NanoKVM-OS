import { useEffect, useState } from 'react';
import { Divider, Modal, Segmented, Tooltip } from 'antd';
import clsx from 'clsx';
import { useSetAtom } from 'jotai';
import { DiscIcon, HardDriveIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/storage.ts';
import { submenuOpenCountAtom } from '@/jotai/settings.ts';
import { useDismissMobileMenu } from '@/components/mobile-menu-context.ts';

import { Images } from './images.tsx';
import { RemoteImage } from './remote.tsx';
import { Tips } from './tips.tsx';

type ImageProps = {
  tooltipPlacement?: 'bottom' | 'left' | 'right';
};

export const Image = ({ tooltipPlacement = 'bottom' }: ImageProps) => {
  const { t } = useTranslation();
  const setSubmenuOpenCount = useSetAtom(submenuOpenCountAtom);
  const dismissMobileMenu = useDismissMobileMenu();

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [isMounted, setIsMounted] = useState(false);
  const [source, setSource] = useState('device');
  const [remoteConnected, setRemoteConnected] = useState(false);
  const [mode, setMode] = useState('cd-rom');

  const modes = [
    {
      value: 'mass-storage',
      label: (
        <div className="flex items-center space-x-1">
          <HardDriveIcon size={16} />
          <span>Mass Storage</span>
        </div>
      )
    },
    {
      value: 'cd-rom',
      label: (
        <div className="flex items-center space-x-1">
          <DiscIcon size={16} />
          <span>CD/DVD</span>
        </div>
      )
    }
  ];

  useEffect(() => {
    let disposed = false;
    Promise.all([api.getMountedImage(), api.getCdRom()])
      .then(([mounted, cdrom]) => {
        if (disposed) return;
        if (mounted.code === 0) {
          const active = !!mounted.data?.file;
          setIsMounted(active);
          // Preserve a connected image's real mode; new mounts default to CD/DVD.
          if (active && cdrom.code === 0) {
            setMode(cdrom.data?.cdrom === 1 ? 'cd-rom' : 'mass-storage');
          }
        }
      })
      .catch(() => {});
    return () => {
      disposed = true;
    };
  }, []);

  useEffect(() => {
    if (remoteConnected) return;
    api
      .getMountedImage()
      .then((rsp) => {
        if (rsp.code === 0) setIsMounted(!!rsp.data?.file);
      })
      .catch(() => {});
  }, [remoteConnected]);

  function toggleModal(open: boolean) {
    setIsModalOpen(open);
    setSubmenuOpenCount((count) => (open ? count + 1 : Math.max(0, count - 1)));
  }

  return (
    <>
      <Tooltip title={t('image.title')} placement={tooltipPlacement} mouseEnterDelay={0.6}>
        <div
          className={clsx(
            'flex h-[30px] w-[30px] cursor-pointer items-center justify-center rounded hover:bg-neutral-700',
            isMounted || remoteConnected ? 'text-blue-500' : 'text-neutral-300 hover:text-white'
          )}
          onClick={() => {
            dismissMobileMenu();
            toggleModal(true);
          }}
        >
          <DiscIcon size={18} />
        </div>
      </Tooltip>

      <Modal open={isModalOpen} footer={null} onCancel={() => toggleModal(false)}>
        <div className="flex items-center space-x-1">
          <span className="text-xl font-bold">{t('image.title')}</span>
          <Tips />
        </div>

        <Divider style={{ margin: '24px 0' }} />

        <div className="flex flex-col space-y-6">
          <Segmented
            className="[&_.ant-segmented-group]:gap-2"
            block
            value={source}
            onChange={setSource}
            options={[
              { value: 'device', label: t('image.remote.device') },
              { value: 'browser', label: t('image.remote.browser') }
            ]}
          />
          <div
            className="flex items-center justify-between"
            style={{ display: source === 'device' ? undefined : 'none' }}
          >
            <span>{t('image.mountMode')}</span>
            <Segmented
              className="[&_.ant-segmented-group]:gap-2"
              value={mode}
              options={modes}
              disabled={isMounted || remoteConnected}
              onChange={setMode}
            />
          </div>
          <div style={{ display: source === 'browser' ? undefined : 'none' }}>
            <RemoteImage isOpen={isModalOpen} onConnected={setRemoteConnected} />
          </div>
          <div
            style={{ display: source === 'device' ? undefined : 'none' }}
            className="flex flex-col gap-6"
          >
            <Divider style={{ margin: '24px 0 0 0' }} />

            <Images
              disabled={remoteConnected}
              isOpen={isModalOpen}
              cdrom={mode === 'cd-rom'}
              setIsMounted={setIsMounted}
            />
          </div>
        </div>
      </Modal>
    </>
  );
};
