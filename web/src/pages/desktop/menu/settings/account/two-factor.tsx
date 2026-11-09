import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Form, Input, message, Modal, QRCode } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/auth.ts';
import { SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

type Status = { enabled: boolean; left: number };
type CheckValues = { password: string; otp: string };
type Check = 'disable' | 'codes';

// The secret in groups of four, as authenticator apps show it.
const grouped = (secret: string) => secret.replace(/(.{4})/g, '$1 ').trim();

/*
 * Two-factor authentication of the signed-in account: an authenticator app
 * (TOTP) and ten recovery codes. Turning it off or replacing the codes needs
 * the password and a code; an administrator or the console
 * (nkos-totp-reset) can turn it off for a lost phone.
 */
export const TwoFactor = () => {
  const { t } = useTranslation();
  const [messageApi, contextHolder] = message.useMessage();
  const [status, setStatus] = useState<Status>();
  const [busy, setBusy] = useState(false);
  const [setup, setSetup] = useState<{ secret: string; uri: string } | null>(null);
  const [setupError, setSetupError] = useState('');
  const [codes, setCodes] = useState<string[] | null>(null);
  const [check, setCheck] = useState<Check | null>(null);
  const [checkError, setCheckError] = useState('');
  const [checkForm] = Form.useForm<CheckValues>();

  const load = useCallback(async () => {
    try {
      const rsp = await api.getAccount();
      if (rsp.code === 0 && rsp.data && rsp.data.totpEnabled !== undefined) {
        setStatus({ enabled: rsp.data.totpEnabled, left: rsp.data.recoveryCodesLeft ?? 0 });
      }
    } catch {
      // An older server without two-factor authentication: no row.
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const codeError = (code: number) =>
    code === api.TOTP_CLOCK_NOT_SET
      ? t('auth.totp.clockNotSet')
      : code === -5 || code === -4
        ? t('auth.locked')
        : code === -3
          ? t('auth.invalidCurrentPassword')
          : t('auth.totp.invalidCode');

  async function startSetup() {
    setBusy(true);
    setSetupError('');
    try {
      const rsp = await api.totpSetup();
      if (rsp.code !== 0 || !rsp.data) throw new Error(rsp.msg);
      setSetup(rsp.data);
    } catch {
      messageApi.error(t('settings.account.totp.failed'));
    } finally {
      setBusy(false);
    }
  }

  async function confirmSetup(values: { otp: string }) {
    setBusy(true);
    try {
      const rsp = await api.totpEnable(values.otp);
      if (rsp.code !== 0 || !rsp.data) {
        setSetupError(
          rsp.code === -2 ? t('settings.account.totp.setupExpired') : codeError(rsp.code)
        );
        return;
      }
      setSetup(null);
      setCodes(rsp.data.recoveryCodes);
      await load();
    } catch {
      setSetupError(t('settings.account.totp.failed'));
    } finally {
      setBusy(false);
    }
  }

  async function runCheck(values: CheckValues) {
    if (!check) return;
    setBusy(true);
    setCheckError('');
    try {
      if (check === 'disable') {
        const rsp = await api.totpDisable(values.password, values.otp);
        if (rsp.code !== 0) {
          setCheckError(codeError(rsp.code));
          return;
        }
        messageApi.success(t('settings.account.totp.disabled'));
      } else {
        const rsp = await api.totpRecoveryCodes(values.password, values.otp);
        if (rsp.code !== 0 || !rsp.data) {
          setCheckError(codeError(rsp.code));
          return;
        }
        setCodes(rsp.data.recoveryCodes);
      }
      setCheck(null);
      checkForm.resetFields();
      await load();
    } catch {
      setCheckError(t('settings.account.totp.failed'));
    } finally {
      setBusy(false);
    }
  }

  function saveCodes() {
    if (!codes) return;
    const blob = new Blob([`NanoKVM recovery codes\n\n${codes.join('\n')}\n`], {
      type: 'text/plain'
    });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = 'nanokvm-recovery-codes.txt';
    link.click();
    URL.revokeObjectURL(url);
  }

  if (!status) return null;

  return (
    <>
      {contextHolder}
      <SettingsSection
        title={t('settings.account.totp.title')}
        description={t('settings.account.totp.description')}
      >
        <SettingRow
          label={t('settings.account.totp.authenticator')}
          description={
            status.enabled
              ? t('settings.account.totp.onDescription', { count: status.left })
              : t('settings.account.totp.offDescription')
          }
        >
          {status.enabled ? (
            <div className="flex flex-wrap justify-end gap-2">
              <Button disabled={busy} onClick={() => setCheck('codes')}>
                {t('settings.account.totp.newCodes')}
              </Button>
              <Button danger disabled={busy} onClick={() => setCheck('disable')}>
                {t('settings.account.totp.turnOff')}
              </Button>
            </div>
          ) : (
            <Button type="primary" loading={busy} onClick={() => void startSetup()}>
              {t('settings.account.totp.setUp')}
            </Button>
          )}
        </SettingRow>
        {status.enabled && status.left <= 2 && (
          <Alert type="warning" showIcon message={t('settings.account.totp.fewCodes')} />
        )}
      </SettingsSection>

      <Modal
        title={t('settings.account.totp.setUp')}
        open={!!setup}
        footer={null}
        destroyOnHidden
        onCancel={() => setSetup(null)}
      >
        {setup && (
          <Form<{ otp: string }> layout="vertical" onFinish={confirmSetup}>
            <p className="text-sm">{t('settings.account.totp.scan')}</p>
            <div className="flex justify-center pb-3">
              <QRCode value={setup.uri} size={192} bgColor="#ffffff" color="#000000" />
            </div>
            <p className="text-fg-muted mb-1 text-xs">{t('settings.account.totp.manual')}</p>
            <p className="mb-4 font-mono text-sm break-all select-all">{grouped(setup.secret)}</p>
            <Form.Item
              name="otp"
              label={t('settings.account.totp.confirmCode')}
              rules={[{ required: true, message: t('auth.totp.required') }]}
            >
              <Input autoComplete="one-time-code" inputMode="numeric" maxLength={8} />
            </Form.Item>
            {setupError && <div className="pb-2 text-red-500">{setupError}</div>}
            <Button type="primary" htmlType="submit" loading={busy} className="w-full">
              {t('settings.account.totp.enable')}
            </Button>
          </Form>
        )}
      </Modal>

      <Modal
        title={t('settings.account.totp.codesTitle')}
        open={!!codes}
        closable={false}
        maskClosable={false}
        destroyOnHidden
        footer={[
          <Button key="save" onClick={saveCodes}>
            {t('settings.account.totp.saveCodes')}
          </Button>,
          <Button
            key="copy"
            onClick={() =>
              void navigator.clipboard
                ?.writeText((codes ?? []).join('\n'))
                .then(() => messageApi.success(t('settings.account.totp.copied')))
            }
          >
            {t('settings.account.totp.copyCodes')}
          </Button>,
          <Button key="done" type="primary" onClick={() => setCodes(null)}>
            {t('settings.account.totp.codesSaved')}
          </Button>
        ]}
      >
        <p className="text-sm">{t('settings.account.totp.codesHint')}</p>
        <ul className="m-0 grid list-none grid-cols-2 gap-x-6 gap-y-1 p-0 font-mono text-sm select-all">
          {(codes ?? []).map((code) => (
            <li key={code}>{code}</li>
          ))}
        </ul>
      </Modal>

      <Modal
        title={
          check === 'disable'
            ? t('settings.account.totp.turnOff')
            : t('settings.account.totp.newCodes')
        }
        open={!!check}
        footer={null}
        destroyOnHidden
        onCancel={() => {
          setCheck(null);
          setCheckError('');
        }}
      >
        <Form<CheckValues> form={checkForm} layout="vertical" onFinish={runCheck}>
          <p className="text-sm">
            {check === 'disable'
              ? t('settings.account.totp.turnOffHint')
              : t('settings.account.totp.newCodesHint')}
          </p>
          <Form.Item
            name="password"
            label={t('auth.placeholderCurrentPassword')}
            rules={[{ required: true, message: t('auth.noEmptyPassword') }]}
          >
            <Input.Password autoComplete="current-password" />
          </Form.Item>
          <Form.Item
            name="otp"
            label={t('auth.totp.placeholder')}
            rules={[{ required: true, message: t('auth.totp.required') }]}
          >
            <Input autoComplete="one-time-code" maxLength={16} />
          </Form.Item>
          {checkError && <div className="pb-2 text-red-500">{checkError}</div>}
          <Button
            type="primary"
            danger={check === 'disable'}
            htmlType="submit"
            loading={busy}
            className="w-full"
          >
            {t('auth.ok')}
          </Button>
        </Form>
      </Modal>
    </>
  );
};
