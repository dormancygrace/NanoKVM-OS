import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Alert,
  Button,
  Collapse,
  ConfigProvider,
  Form,
  Input,
  InputNumber,
  message,
  Popconfirm,
  Select,
  Space,
  Switch,
  Tag,
  Typography
} from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import {
  configureRustDesk,
  getRustDeskStatus,
  rustDeskPackageAction,
  type RustDeskConfig,
  type RustDeskStatus
} from '@/api/rustdesk';
import { pollWhileVisible } from '@/lib/visible-poll';
import { rustDeskStatusAtom } from '@/jotai/rustdesk';
import { RustDeskIcon } from '@/components/icons/rustdesk';

import { RustDeskVersions } from '../software/rustdesk-versions';

export const RustDeskControls = () => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.rustdesk' });
  const [status, setStatus] = useAtom(rustDeskStatusAtom);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm<RustDeskConfig>();
  const dirty = useRef(false);
  const mounted = useRef(false);
  const working = useRef(false);
  const pending = useRef(false);
  const generation = useRef(0);
  const statusRequest = useRef<AbortController | null>(null);
  const official = Form.useWatch('use_official_id_server', form);
  const passwordMode = Form.useWatch('password_mode', form);
  const audioEnabled = Form.useWatch('audio_enabled', form);
  const [protocolMajor, protocolMinor] = (status?.rustdesk_version || '0.0').split('.').map(Number);
  const rotatesOnLogin = protocolMajor > 1 || (protocolMajor === 1 && protocolMinor >= 5);
  const refresh = useCallback(async () => {
    if (!mounted.current || working.current || pending.current) return;
    pending.current = true;
    const started = generation.current;
    const controller = new AbortController();
    statusRequest.current = controller;
    try {
      const response = await getRustDeskStatus(controller.signal);
      if (started !== generation.current) return;
      if (response.code !== 0) {
        setError(response.msg);
        return;
      }
      const next = response.data as RustDeskStatus;
      setStatus(next);
      if (!dirty.current)
        form.setFieldsValue({ audio_enabled: true, ...next.config, password: '' });
      setError('');
    } catch {
      if (started === generation.current && !controller.signal.aborted) setError(t('failed'));
    } finally {
      if (started === generation.current) pending.current = false;
    }
  }, [form, setStatus, t]);

  useEffect(() => {
    mounted.current = true;
    void refresh();
    const stop = pollWhileVisible(() => void refresh(), 4000);
    const invalidate = () => {
      generation.current++;
      statusRequest.current?.abort();
      pending.current = false;
    };
    return () => {
      mounted.current = false;
      invalidate();
      stop();
    };
  }, [refresh]);

  const operation = async (action: 'regenerate-password' | 'save') => {
    if (!mounted.current || working.current) return;
    const entered = generation.current;
    let values: RustDeskConfig | undefined;
    if (action === 'save') {
      try {
        values = await form.validateFields();
      } catch {
        return;
      }
    }
    if (!mounted.current || entered !== generation.current) return;
    const started = ++generation.current;
    statusRequest.current?.abort();
    pending.current = false;
    working.current = true;
    setBusy(true);
    try {
      const response =
        action === 'save' ? await configureRustDesk(values!) : await rustDeskPackageAction(action);
      if (!mounted.current || started !== generation.current) return;
      if (response.code !== 0) {
        setError(response.msg);
        return;
      }
      if (action !== 'regenerate-password') dirty.current = false;
      message.success(action === 'save' ? t('saved') : t('passwordUpdated'));
      setError('');
    } catch {
      if (mounted.current && started === generation.current) setError(t('failed'));
    } finally {
      working.current = false;
      if (mounted.current) {
        setBusy(false);
        if (started === generation.current) await refresh();
      }
    }
  };

  return (
    <div className="min-w-0">
      <h2 className="mb-5 flex items-center gap-2 text-xl">
        <RustDeskIcon size={24} />
        {t('settings')}
      </h2>
      <Space direction="vertical" size="middle" className="w-full">
        {error && <Alert type="error" title={error} showIcon />}
        {status?.installed && <RustDeskVersions status={status} />}
        <div className="flex flex-wrap items-center gap-2">
          {status?.installed && (
            <Tag color={status.running ? 'green' : 'default'}>
              {status.running ? t('running') : t('stopped')}
            </Tag>
          )}
          {status?.running && (
            <Tag color={status.runtime?.registered ? 'green' : 'orange'}>
              {status.runtime?.registered ? t('registered') : t('registering')}
            </Tag>
          )}
        </div>

        {status?.id && (
          <div className="break-all">
            <strong>{t('id')}: </strong>
            <Typography.Text copyable>{status.id}</Typography.Text>
          </div>
        )}
        {status?.installed && status.config.password_mode === 'temporary' && (
          <div>
            {status.temporary_password ? (
              <div className="flex flex-wrap items-center gap-2">
                <strong>{t('temporaryPassword')}: </strong>
                <Typography.Text code copyable>
                  {status.temporary_password}
                </Typography.Text>
                <Popconfirm
                  title={t('regenerateConfirm')}
                  onConfirm={() => operation('regenerate-password')}
                >
                  <Button disabled={busy || !status.running}>{t('newPassword')}</Button>
                </Popconfirm>
              </div>
            ) : (
              <p>{status.running ? t('waitingForPassword') : t('startForPassword')}</p>
            )}
            <p className="mt-2 text-sm text-neutral-400">
              {rotatesOnLogin ? t('rotatingTemporaryHint') : t('temporaryHint')}
            </p>
          </div>
        )}
        <p>{t('explain')}</p>
        {status?.installed && (
          <ConfigProvider
            theme={{
              token: { colorBgContainer: '#262626', colorBorder: '#525252' },
              components: {
                Collapse: { headerPadding: '12px 16px', contentPadding: 16 }
              }
            }}
          >
            <Form
              form={form}
              initialValues={{ audio_enabled: true, ...status.config, password: '' }}
              layout="vertical"
              disabled={busy}
              onValuesChange={() => {
                dirty.current = true;
              }}
            >
              <Form.Item
                name="service_enabled"
                label={t('enabled')}
                valuePropName="checked"
                extra={t('inputDefaults')}
              >
                <Switch />
              </Form.Item>
              {status.supports_audio_settings && (
                <Form.Item
                  name="audio_enabled"
                  label={t('transmitAudio')}
                  valuePropName="checked"
                  extra={t('audioHint')}
                >
                  <Switch />
                </Form.Item>
              )}
              {status.supports_audio && (
                <Alert
                  type="info"
                  title={
                    status.supports_audio_settings && audioEnabled === false
                      ? t('audioMuted')
                      : status.usb_audio_enabled
                        ? t('audioEnabled')
                        : status.supports_audio_settings
                          ? t('audioHint')
                          : t('audioDisabled')
                  }
                  className="mb-4"
                />
              )}
              <Form.Item name="password_mode" label={t('passwordMode')}>
                <Select
                  options={[
                    { value: 'temporary', label: t('temporary') },
                    { value: 'permanent', label: t('permanent') }
                  ]}
                />
              </Form.Item>
              {passwordMode === 'permanent' && (
                <Form.Item
                  name="password"
                  label={t('password')}
                  rules={[
                    {
                      validator: async (_, value: string) => {
                        if (
                          form.getFieldValue('password_mode') !== 'permanent' ||
                          (!value && status.has_password)
                        )
                          return;
                        const length = new TextEncoder().encode(value || '').length;
                        if (length < 8 || length > 64) throw new Error(t('passwordRequired'));
                      }
                    }
                  ]}
                >
                  <Input.Password
                    autoComplete="new-password"
                    placeholder={status.has_password ? t('keepPassword') : ''}
                  />
                </Form.Item>
              )}
              <Collapse
                className="mb-4"
                items={[
                  {
                    key: 'advanced',
                    label: t('advanced'),
                    forceRender: true,
                    children: (
                      <>
                        <Form.Item
                          name="use_official_id_server"
                          label={t('server')}
                          getValueProps={(value) => ({ value: value ? 'official' : 'custom' })}
                          getValueFromEvent={(value) => value === 'official'}
                        >
                          <Select
                            options={[
                              { value: 'official', label: t('public') },
                              { value: 'custom', label: t('custom') }
                            ]}
                          />
                        </Form.Item>
                        {!official && (
                          <>
                            <Alert type="info" title={t('sameServer')} className="mb-4" />
                            <Form.Item
                              name="rendezvous_server"
                              label={t('idServer')}
                              rules={[{ required: true }]}
                            >
                              <Input placeholder="id.example.com:21116" autoComplete="off" />
                            </Form.Item>
                            <Form.Item name="relay_server" label={t('relay')}>
                              <Input placeholder="relay.example.com:21117" autoComplete="off" />
                            </Form.Item>
                            <Form.Item name="server_key" label={t('key')}>
                              <Input autoComplete="off" />
                            </Form.Item>
                          </>
                        )}
                        {status.supports_transport_settings && (
                          <Form.Item
                            name="webrtc_enabled"
                            label={t('webrtc')}
                            valuePropName="checked"
                            extra={t('transportHint')}
                          >
                            <Switch />
                          </Form.Item>
                        )}
                        <Alert type="info" title={t('video')} className="mb-4" />
                        <Form.Item
                          name="max_clients"
                          label={t('clients')}
                          rules={[{ required: true }]}
                          className="mb-0"
                        >
                          <InputNumber min={1} max={8} />
                        </Form.Item>
                      </>
                    )
                  }
                ]}
              />
              <Button type="primary" loading={busy} onClick={() => void operation('save')}>
                {t('save')}
              </Button>
            </Form>
          </ConfigProvider>
        )}
        {status?.installed && status.source_url && (
          <a href={status.source_url} target="_blank" rel="noopener noreferrer">
            {t('source')} · AGPL-3.0
          </a>
        )}
      </Space>
    </div>
  );
};
