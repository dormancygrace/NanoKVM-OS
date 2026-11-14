import { useState } from 'react';
import { Checkbox, Modal } from 'antd';
import { Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/tailscale.ts';
import { showRequestError } from '@/lib/show-request-error.ts';

type UninstallProps = {
  onSuccess: () => void;
};

export const Uninstall = ({ onSuccess }: UninstallProps) => {
  const { t } = useTranslation();

  const [isLoading, setIsLoading] = useState(false);
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [purge, setPurge] = useState(false);

  function uninstall() {
    if (isLoading) return;
    setIsLoading(true);

    api
      .uninstall(purge)
      .then((rsp) => {
        if (rsp.code !== 0) showRequestError(rsp);
      })
      .catch((err) => showRequestError(err))
      .finally(() => {
        setIsModalOpen(false);
        setIsLoading(false);
        onSuccess();
      });
  }

  const title = (
    <div className="text-danger flex items-center gap-1">
      <Trash2Icon size={16} />
      <span>{t('settings.tailscale.uninstall')}</span>
    </div>
  );

  return (
    <>
      <button
        type="button"
        className="nanokvm-button-base text-fg hover:bg-surface-raised flex h-[30px] w-full cursor-pointer items-center space-x-1 rounded px-2 py-1"
        onClick={() => {
          setPurge(false);
          setIsModalOpen(true);
        }}
      >
        <span>{t('settings.tailscale.uninstall')}</span>
      </button>

      <Modal
        title={title}
        open={isModalOpen}
        centered={true}
        okType="danger"
        okText={t('settings.tailscale.okBtn')}
        cancelText={t('settings.tailscale.cancelBtn')}
        onOk={uninstall}
        onCancel={() => setIsModalOpen(false)}
        confirmLoading={isLoading}
      >
        <div className="py-5">
          <p className="text-base">{t('settings.tailscale.uninstallDesc')}</p>
          <Checkbox
            className="pt-3"
            checked={purge}
            disabled={isLoading}
            onChange={(e) => setPurge(e.target.checked)}
          >
            {t('settings.tailscale.uninstallPurge')}
          </Checkbox>
        </div>
      </Modal>
    </>
  );
};
