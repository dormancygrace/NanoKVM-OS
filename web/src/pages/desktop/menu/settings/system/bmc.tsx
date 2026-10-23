import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Form, Input, InputNumber, message, Spin, Switch } from 'antd';
import { DownloadIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  configureBmc,
  getBmcStatus,
  ipmiPackageAction,
  type BmcSettings,
  type BmcStatus
} from '@/api/bmc.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

// System > Redfish and IPMI: Redfish is part of the server, IPMI comes with
// its add-on; one BMC account for both, and the power button hold of a
// forced power-off.
export const BmcSettingsPage = () => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.bmc' });
  const [status, setStatus] = useState<BmcStatus>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [showValidationErrors, setShowValidationErrors] = useState(false);
  const [form] = Form.useForm<BmcSettings>();
  const dirty = useRef(false);
  const mounted = useRef(false);
  const ipmi = Form.useWatch('ipmi', form);
  const redfish = Form.useWatch('redfish', form);

  const refresh = useCallback(async () => {
    if (!mounted.current) return;
    try {
      const response = await getBmcStatus();
      if (!mounted.current) return;
      if (response.code !== 0) throw new Error(response.msg);
      const next = response.data as BmcStatus;
      setStatus(next);
      if (!dirty.current)
        form.setFieldsValue({ ...next, holdSeconds: next.holdSeconds || undefined, password: '' });
      setError('');
    } catch {
      if (mounted.current) setError(t('failed'));
    }
  }, [form, t]);

  useEffect(() => {
    mounted.current = true;
    void refresh();
    const stop = pollWhileVisible(() => void refresh(), 5000);
    return () => {
      mounted.current = false;
      stop();
    };
  }, [refresh]);

  const save = async (values: BmcSettings) => {
    setBusy(true);
    try {
      const response = await configureBmc({
        ...values,
        ipmi: Boolean(values.ipmi),
        // The port field exists only with the add-on installed.
        ipmiPort: values.ipmiPort ?? status?.ipmiPort ?? 0,
        // Empty: the power menu's long press.
        holdSeconds: values.holdSeconds || 0,
        powerLedConnected: values.powerLedConnected !== false,
        password: values.password ?? ''
      });
      if (response.code !== 0) throw new Error(response.msg);
      dirty.current = false;
      setShowValidationErrors(false);
      form.setFieldValue('password', '');
      void message.success(t('saved'));
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : t('failed'));
    } finally {
      setBusy(false);
      void refresh();
    }
  };

  const installIpmi = async () => {
    setInstalling(true);
    try {
      const response = await ipmiPackageAction('install');
      if (response.code !== 0) throw new Error(response.msg);
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : t('failed'));
    } finally {
      setInstalling(false);
      void refresh();
    }
  };

  if (!status) return error ? <Alert type="error" title={error} showIcon /> : <Spin />;
  // The account and the power options serve Redfish and IPMI: without
  // either they configure nothing. Hidden, not unmounted, so a save keeps them.
  const shared = Boolean(redfish) || status.ipmiInstalled;

  return (
    <div className="space-y-6">
      <p className="text-fg-muted m-0 text-sm">{t('description')}</p>
      {error && <Alert type="error" title={error} showIcon />}
      <Form
        form={form}
        layout="vertical"
        onValuesChange={() => {
          dirty.current = true;
        }}
        onFinish={(values) => void save(values)}
        onFinishFailed={() => setShowValidationErrors(true)}
      >
        <SettingsSection title={t('redfish')}>
          <Form.Item
            name="redfish"
            label={t('redfishEnable')}
            valuePropName="checked"
            extra={t('redfishHint', { host: window.location.host })}
          >
            <Switch />
          </Form.Item>
        </SettingsSection>
        <SettingsSection title={t('ipmi')}>
          {status.ipmiInstalled ? (
            <>
              <StatusBadge tone={status.ipmiRunning ? 'success' : 'neutral'}>
                {status.ipmiRunning ? t('running') : t('stopped')}
              </StatusBadge>
              <Form.Item
                name="ipmi"
                label={t('ipmiEnable')}
                valuePropName="checked"
                extra={t('ipmiHint')}
              >
                <Switch />
              </Form.Item>
              {ipmi && <Alert type="warning" title={t('ipmiWarning')} showIcon />}
              <Form.Item name="ipmiPort" label={t('ipmiPort')}>
                <InputNumber min={1} max={65535} />
              </Form.Item>
            </>
          ) : (
            <>
              <p className="text-fg-muted m-0 text-sm">{t('ipmiNotInstalled')}</p>
              <Button
                icon={<DownloadIcon size={16} />}
                loading={installing}
                onClick={() => void installIpmi()}
              >
                {t('ipmiInstall')}
              </Button>
            </>
          )}
        </SettingsSection>
        {!shared && !showValidationErrors && (
          <p className="text-fg-muted m-0 text-sm">{t('sharedOff')}</p>
        )}
        {/* A rejected draft must stay editable even after Redfish is switched off. */}
        <div hidden={!shared && !showValidationErrors}>
          <SettingsSection
            title={t('account')}
            description={status.ipmiInstalled ? t('accountHint') : t('accountHintRedfish')}
          >
            <Form.Item
              name="username"
              label={t('username')}
              rules={[
                { required: true, pattern: /^[A-Za-z0-9._-]{1,16}$/, message: t('usernameRule') }
              ]}
            >
              <Input autoComplete="off" maxLength={16} />
            </Form.Item>
            <Form.Item
              name="password"
              label={t('password')}
              extra={status.passwordSet ? t('passwordKeep') : t('passwordRule')}
              rules={[
                { min: 8, message: t('passwordRule') },
                { max: 20, message: t('passwordRule') }
              ]}
            >
              <Input.Password autoComplete="new-password" maxLength={20} />
            </Form.Item>
            <Form.Item name="holdSeconds" label={t('holdSeconds')} extra={t('holdHint')}>
              <InputNumber min={1} max={30} placeholder={t('holdDefault')} />
            </Form.Item>
            <Form.Item
              name="powerLedConnected"
              label={t('powerLed')}
              valuePropName="checked"
              extra={t('powerLedHint')}
            >
              <Switch />
            </Form.Item>
          </SettingsSection>
        </div>
        <Button type="primary" htmlType="submit" loading={busy}>
          {t('save')}
        </Button>
      </Form>
    </div>
  );
};
