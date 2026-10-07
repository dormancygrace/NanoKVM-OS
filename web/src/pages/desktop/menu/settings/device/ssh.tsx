import { useEffect, useState } from 'react';
import { Button, Form, Input, Modal, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { encrypt } from '@/lib/encrypt.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

type PasswordForm = { password: string; confirmation: string };

export const Ssh = () => {
  const { t } = useTranslation();
  const [form] = Form.useForm<PasswordForm>();
  const [isEnabled, setIsEnabled] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const [passwordOpen, setPasswordOpen] = useState(false);

  useEffect(() => {
    setIsLoading(true);
    api
      .getSSHState()
      .then((rsp) => setIsEnabled(!!rsp.data?.enabled))
      .finally(() => setIsLoading(false));
  }, []);

  async function disable() {
    setIsLoading(true);
    try {
      const rsp = await api.disableSSH();
      if (rsp.code === 0) setIsEnabled(false);
    } finally {
      setIsLoading(false);
    }
  }

  async function enable(values: PasswordForm) {
    if (values.password.trim().toLowerCase() === 'root') {
      form.setFields([{ name: 'password', errors: [t('settings.system.ssh.rootForbidden')] }]);
      return;
    }
    setIsLoading(true);
    try {
      const rsp = await api.enableSSH(encrypt(values.password));
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.system.ssh.failed');
        return;
      }
      setIsEnabled(true);
      setPasswordOpen(false);
      form.resetFields();
    } catch (err) {
      showRequestError(err, 'settings.system.ssh.failed');
    } finally {
      setIsLoading(false);
    }
  }

  return (
    <>
      <SettingRow
        label="SSH"
        description={t('settings.system.ssh.description')}
        help={t('settings.system.ssh.tip')}
        htmlFor="system-ssh"
      >
        <Switch
          id="system-ssh"
          aria-describedby="system-ssh-description"
          checked={isEnabled}
          loading={isLoading}
          onChange={(next) => (next ? setPasswordOpen(true) : void disable())}
        />
      </SettingRow>
      <Modal
        open={passwordOpen}
        title={t('settings.system.ssh.passwordTitle')}
        footer={null}
        destroyOnHidden
        onCancel={() => {
          if (!isLoading) {
            setPasswordOpen(false);
            form.resetFields();
          }
        }}
      >
        <p className="text-fg-muted mb-5 text-sm">{t('settings.system.ssh.passwordDescription')}</p>
        <Form form={form} layout="vertical" onFinish={enable}>
          <Form.Item
            name="password"
            label={t('settings.system.ssh.password')}
            rules={[
              { required: true, message: t('auth.noEmptyPassword') },
              { min: 8, max: 72, message: t('auth.passwordLength') },
              {
                validator: (_, value) =>
                  value?.trim().toLowerCase() === 'root'
                    ? Promise.reject(new Error(t('settings.system.ssh.rootForbidden')))
                    : Promise.resolve()
              }
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item
            name="confirmation"
            label={t('settings.system.ssh.confirmation')}
            dependencies={['password']}
            rules={[
              { required: true, message: t('auth.noEmptyPassword') },
              ({ getFieldValue }) => ({
                validator: (_, value) =>
                  !value || getFieldValue('password') === value
                    ? Promise.resolve()
                    : Promise.reject(new Error(t('auth.differentPassword')))
              })
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <div className="flex justify-end gap-2">
            <Button disabled={isLoading} onClick={() => setPasswordOpen(false)}>
              {t('auth.cancel')}
            </Button>
            <Button type="primary" htmlType="submit" loading={isLoading}>
              {t('settings.system.ssh.enable')}
            </Button>
          </div>
        </Form>
      </Modal>
    </>
  );
};
