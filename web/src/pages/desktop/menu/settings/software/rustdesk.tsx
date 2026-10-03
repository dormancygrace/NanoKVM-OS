import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Card, Form, Input, InputNumber, Popconfirm, Select, Space, Switch, Tag, message } from 'antd';
import { useTranslation } from 'react-i18next';

import { configureRustDesk, getRustDeskStatus, rustDeskPackageAction, type RustDeskConfig, type RustDeskStatus } from '@/api/rustdesk';
import { getBaseUrl } from '@/lib/service';
import { pollWhileVisible } from '@/lib/visible-poll';

const labels = {
  en: {
    installed: 'Installed', absent: 'Not installed', running: 'Running', stopped: 'Stopped',
    registered: 'Registered', registering: 'Waiting for ID server',
    install: 'Install', remove: 'Remove', upgrade: 'Upgrade', save: 'Save settings',
    enabled: 'Enable remote access', server: 'Server', public: 'Official public servers',
    custom: 'Custom server', idServer: 'ID / rendezvous server', relay: 'Relay server (optional)',
    key: 'Server public key (optional)', password: 'Access password',
    keepPassword: 'Leave empty to keep the current password', codec: 'Video codec',
    clients: 'Maximum viewers', unavailable: 'RustDesk is not available in the configured APK repositories. Add the repository containing nanokvm-rustdesk, or install its APK through Packages.',
    explain: 'Connect a RustDesk client to this ID to view HDMI and control USB keyboard and mouse.',
    video: 'The codec must match the shared NanoKVM encoder. H.265 requires client support. Maximum portrait output needs H.265. Browser takeover releases RustDesk input.',
    deletion: 'Remove the package? Server settings, password and device ID are preserved.',
    failed: 'The request failed', saved: 'Settings saved', done: 'Package operation completed',
    id: 'RustDesk ID', source: 'Source and license', passwordRequired: 'Enter a password of 8 to 64 bytes',
    sameServer: 'Configure the same custom ID server and public key in the RustDesk client.',
  },
  ru: {
    installed: 'Установлен', absent: 'Не установлен', running: 'Работает', stopped: 'Остановлен',
    registered: 'Зарегистрирован', registering: 'Ожидание ID-сервера',
    install: 'Установить', remove: 'Удалить', upgrade: 'Обновить', save: 'Сохранить настройки',
    enabled: 'Включить удалённый доступ', server: 'Сервер', public: 'Штатные публичные серверы',
    custom: 'Собственный сервер', idServer: 'ID / rendezvous сервер', relay: 'Relay сервер (необязательно)',
    key: 'Публичный ключ сервера (необязательно)', password: 'Пароль доступа',
    keepPassword: 'Оставьте пустым, чтобы сохранить пароль', codec: 'Видеокодек',
    clients: 'Максимум зрителей', unavailable: 'В настроенных APK-репозиториях нет RustDesk. Добавьте репозиторий с nanokvm-rustdesk или установите его APK через Packages.',
    explain: 'Подключитесь к этому ID из RustDesk, чтобы видеть HDMI и управлять USB-клавиатурой и мышью.',
    video: 'Кодек должен совпадать с общим энкодером NanoKVM. Для H.265 нужна поддержка клиента. Максимальный портретный режим требует H.265. Перехват управления браузером освобождает ввод RustDesk.',
    deletion: 'Удалить пакет? Настройки сервера, пароль и ID устройства сохранятся.',
    failed: 'Запрос не выполнен', saved: 'Настройки сохранены', done: 'Операция с пакетом завершена',
    id: 'RustDesk ID', source: 'Исходники и лицензия', passwordRequired: 'Введите пароль от 8 до 64 байт',
    sameServer: 'Укажите тот же собственный ID-сервер и публичный ключ в клиенте RustDesk.',
  },
};

export const RustDeskAddon = () => {
  const { i18n } = useTranslation();
  const l = labels[(i18n.resolvedLanguage || i18n.language).startsWith('ru') ? 'ru' : 'en'];
  const [status, setStatus] = useState<RustDeskStatus>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm<RustDeskConfig>();
  const dirty = useRef(false);
  const working = useRef(false);
  const official = Form.useWatch('use_official_id_server', form);
  const refresh = useCallback(async () => {
    if (working.current) return;
    try {
      const response = await getRustDeskStatus();
      if (response.code !== 0) { setError(response.msg); return; }
      const next = response.data as RustDeskStatus;
      setStatus(next);
      if (!dirty.current) form.setFieldsValue({ ...next.config, password: '' });
      setError('');
    } catch { setError(l.failed); }
  }, [form, l.failed]);

  useEffect(() => {
    void refresh();
    return pollWhileVisible(() => void refresh(), 4000);
  }, [refresh]);

  const operation = async (action: 'install' | 'upgrade' | 'remove' | 'save') => {
    if (working.current) return;
    let values: RustDeskConfig | undefined;
    if (action === 'save') {
      try { values = await form.validateFields(); } catch { return; }
    }
    working.current = true;
    setBusy(true);
    try {
      const response = action === 'save' ? await configureRustDesk(values!) : await rustDeskPackageAction(action);
      if (response.code !== 0) { setError(response.msg); return; }
      dirty.current = false;
      message.success(action === 'save' ? l.saved : l.done);
      setError('');
    } catch { setError(l.failed); }
    finally { working.current = false; setBusy(false); await refresh(); }
  };

  return (
    <Card title="RustDesk" loading={!status && !error} className="min-w-0">
      <Space direction="vertical" size="middle" className="w-full">
        {error && <Alert type="error" title={error} showIcon />}
        <div className="flex flex-wrap items-center gap-2">
          <Tag>{status?.installed ? l.installed : l.absent}</Tag>
          {status?.installed && <Tag color={status.running ? 'green' : 'default'}>{status.running ? l.running : l.stopped}</Tag>}
          {status?.running && <Tag color={status.runtime?.registered ? 'green' : 'orange'}>{status.runtime?.registered ? l.registered : l.registering}</Tag>}
          {status?.installed ? <>
            <Button disabled={busy} onClick={() => void operation('upgrade')}>{l.upgrade}</Button>
            <Popconfirm title={l.deletion} onConfirm={() => operation('remove')}>
              <Button danger disabled={busy}>{l.remove}</Button>
            </Popconfirm>
          </> : <Button type="primary" loading={busy} disabled={!status?.available} onClick={() => void operation('install')}>{l.install}</Button>}
        </div>
        {!status?.installed && status && !status.available && <Alert type="info" title={l.unavailable} />}
        {status?.id && <div className="break-all"><strong>{l.id}: </strong>{status.id}</div>}
        <p>{l.explain}</p>
        {status?.installed && <Form form={form} layout="vertical" disabled={busy} onValuesChange={() => { dirty.current = true; }}>
          <Form.Item name="service_enabled" label={l.enabled} valuePropName="checked"><Switch /></Form.Item>
          <Form.Item name="use_official_id_server" label={l.server} getValueProps={(value) => ({ value: value ? "official" : "custom" })} getValueFromEvent={(value) => value === "official"}>
            <Select options={[{ value: 'official', label: l.public }, { value: 'custom', label: l.custom }]} />
          </Form.Item>
          {!official && <>
            <Alert type="info" title={l.sameServer} className="mb-4" />
            <Form.Item name="rendezvous_server" label={l.idServer} rules={[{ required: true }]}><Input placeholder="id.example.com:21116" autoComplete="off" /></Form.Item>
            <Form.Item name="relay_server" label={l.relay}><Input placeholder="relay.example.com:21117" autoComplete="off" /></Form.Item>
            <Form.Item name="server_key" label={l.key}><Input autoComplete="off" /></Form.Item>
          </>}
          <Form.Item name="password" label={l.password} rules={[{ validator: async (_, value: string) => {
            if (!value && status.has_password) return;
            const length = new TextEncoder().encode(value || '').length;
            if (length < 8 || length > 64) throw new Error(l.passwordRequired);
          } }]}>
            <Input.Password autoComplete="new-password" placeholder={status.has_password ? l.keepPassword : ''} />
          </Form.Item>
          <Alert type="info" title={l.video} className="mb-4" />
          <Form.Item name="codec" label={l.codec}><Select options={[{ value: 'h264', label: 'H.264' }, { value: 'h265', label: 'H.265' }]} /></Form.Item>
          <Form.Item name="max_clients" label={l.clients} rules={[{ required: true }]}><InputNumber min={1} max={8} /></Form.Item>
          <Button type="primary" loading={busy} onClick={() => void operation('save')}>{l.save}</Button>
        </Form>}
        {status?.installed && <a href={getBaseUrl('http') + '/api/addons/rustdesk/source'}>{l.source} · AGPL-3.0</a>}
      </Space>
    </Card>
  );
};
