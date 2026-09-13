import { useCallback, useEffect, useRef, useState } from 'react';
import { Divider, Modal, Segmented, Tooltip } from 'antd';
import clsx from 'clsx';
import { useSetAtom } from 'jotai';
import { DiscIcon, HardDriveIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/storage.ts';
import { submenuOpenCountAtom } from '@/jotai/settings.ts';
import { useVirtualDisk } from '@/hooks/useVirtualDisk.ts';
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

  const diskEnabled = useVirtualDisk();
  const disabled = diskEnabled !== true;
  const openRef = useRef(false);
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

  const toggleModal = useCallback(
    (open: boolean) => {
      if (open === openRef.current) return;
      openRef.current = open;
      setIsModalOpen(open);
      setSubmenuOpenCount((count) => (open ? count + 1 : Math.max(0, count - 1)));
    },
    [setSubmenuOpenCount]
  );

  useEffect(() => {
    if (disabled) toggleModal(false);
  }, [disabled, toggleModal]);
  useEffect(
    () => () => {
      if (openRef.current) setSubmenuOpenCount((count) => Math.max(0, count - 1));
    },
    [setSubmenuOpenCount]
  );

  function openModal() {
    if (disabled) return;
    dismissMobileMenu();
    toggleModal(true);
  }
  if (disabled) return null;

  return (
    <>
      <Tooltip title={t('image.title')} placement={tooltipPlacement} mouseEnterDelay={0.6}>
        <div
          role="button"
          aria-label={t('image.title')}
          aria-disabled={disabled}
          tabIndex={disabled ? -1 : 0}
          className={clsx(
            'flex h-[30px] w-[30px] items-center justify-center rounded',
            disabled
              ? 'cursor-not-allowed text-neutral-600 opacity-45'
              : [
                  'cursor-pointer hover:bg-neutral-700',
                  isMounted || remoteConnected
                    ? 'text-blue-500'
                    : 'text-neutral-300 hover:text-white'
                ]
          )}
          onClick={openModal}
          onKeyDown={(event) => {
            if (event.key === 'Enter' || event.key === ' ') {
              event.preventDefault();
              openModal();
            }
          }}
        >
          <DiscIcon size={18} />
        </div>
      </Tooltip>

      <Modal open={isModalOpen && !disabled} footer={null} onCancel={() => toggleModal(false)}>
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
