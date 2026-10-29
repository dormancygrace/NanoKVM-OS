import { Button, Card, Modal, Typography } from 'antd';
import { useTranslation } from 'react-i18next';

const { Text } = Typography;
export default function TipsDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <Modal
      title={t('auth.forgetPassword')}
      open={open}
      onCancel={onClose}
      closeIcon={null}
      footer={null}
      centered={true}
    >
      <Card style={{ marginTop: '20px' }}>
        <div className="flex w-full flex-col space-y-5">
          <div>{t('auth.tips.reset1')}</div>

          <div className="flex items-center space-x-1">
            <span>{t('auth.tips.reset2')}</span>
            <a
              href="https://wiki.sipeed.com/hardware/en/kvm/NanoKVM/reset.html"
              target="_blank"
              rel="noreferrer"
            >
              wiki
            </a>
          </div>

          <ul className="list-outside list-disc">
            <li>
              {t('auth.tips.reset3')}
              <Text code={true}>admin/admin</Text>
            </li>
            <li>
              {t('auth.tips.reset4')}
              <Text code={true}>root/root</Text>
            </li>
          </ul>
        </div>
      </Card>

      <div className="flex justify-center pt-10 pb-3">
        <Button type="primary" className="w-24" onClick={onClose}>
          {t('auth.ok')}
        </Button>
      </div>
    </Modal>
  );
}
