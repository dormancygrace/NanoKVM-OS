import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Alert,
  Button,
  Checkbox,
  Dropdown,
  Input,
  message,
  Popconfirm,
  Select,
  Spin,
  Switch
} from 'antd';
import { ChevronDownIcon, PlusIcon, SendIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  getNotifyConfig,
  saveNotifyConfig,
  testNotifyChannel,
  type NotifyChannelType,
  type NotifyChannelUpdate,
  type NotifyField,
  type NotifyKind,
  type NotifyStatus
} from '@/api/notify.ts';
import { IconButton, Panel, SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

// Product names stay as they are; the generic types are translated.
const brandNames: Record<string, string> = {
  telegram: 'Telegram',
  slack: 'Slack · Mattermost · Rocket.Chat',
  teams: 'Microsoft Teams',
  googlechat: 'Google Chat',
  discord: 'Discord',
  ntfy: 'ntfy',
  gotify: 'Gotify',
  pushover: 'Pushover',
  matrix: 'Matrix',
  syslog: 'Syslog'
};

const languageNames: Record<string, string> = { en: 'English', ru: 'Русский' };

type Draft = NotifyChannelUpdate & {
  secretsSet: string[];
  // React key: a new channel has no id until it is saved.
  key: string;
  open: boolean;
};

let nextKey = 0;
const draftOf = (status: NotifyStatus): Draft[] =>
  status.channels.map((channel) => ({
    ...channel,
    settings: { ...channel.settings },
    clear: [],
    key: `k${nextKey++}`,
    open: false
  }));

const update = (draft: Draft): NotifyChannelUpdate => ({
  id: draft.id,
  type: draft.type,
  name: draft.name,
  enabled: draft.enabled,
  events: draft.events,
  settings: draft.settings,
  clear: draft.clear
});

// Services > Notifications: channels that get messages about the host and
// the device. Each channel picks its events; Test sends through a channel as
// it is on the page, Save keeps the whole list.
export const Notifications = () => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.notifications' });
  const [status, setStatus] = useState<NotifyStatus>();
  const [channels, setChannels] = useState<Draft[]>([]);
  const [language, setLanguage] = useState('en');
  const [loadError, setLoadError] = useState('');
  const [saveError, setSaveError] = useState('');
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);
  const mounted = useRef(false);

  const show = useCallback((next: NotifyStatus) => {
    setStatus(next);
    setChannels(draftOf(next));
    setLanguage(next.language);
    setDirty(false);
  }, []);

  useEffect(() => {
    mounted.current = true;
    getNotifyConfig()
      .then((response) => {
        if (!mounted.current) return;
        if (response.code !== 0) throw new Error(response.msg);
        show(response.data);
      })
      .catch(() => mounted.current && setLoadError(t('failed')));
    return () => {
      mounted.current = false;
    };
  }, [show, t]);

  const typeName = (type: string) => brandNames[type] ?? t(`types.${type}`);

  const change = (key: string, patch: Partial<Draft>) => {
    setChannels((list) => list.map((item) => (item.key === key ? { ...item, ...patch } : item)));
    setDirty(true);
  };

  const add = (type: string) => {
    setChannels((list) => [
      ...list,
      {
        id: '',
        type,
        name: '',
        enabled: true,
        events: [...(status?.events ?? [])],
        settings: {},
        clear: [],
        secretsSet: [],
        key: `k${nextKey++}`,
        open: true
      }
    ]);
    setDirty(true);
  };

  const remove = (key: string) => {
    setChannels((list) => list.filter((item) => item.key !== key));
    setDirty(true);
  };

  const save = async () => {
    setSaving(true);
    try {
      const response = await saveNotifyConfig({ language, channels: channels.map(update) });
      if (!mounted.current) return;
      if (response.code !== 0) throw new Error(response.msg);
      // Keep the folds the way they are: the order of the list is kept.
      const open = channels.map((item) => item.open);
      show(response.data);
      setChannels((list) => list.map((item, i) => ({ ...item, open: open[i] ?? false })));
      setSaveError('');
      void message.success(t('saved'));
    } catch (err) {
      if (mounted.current) setSaveError(err instanceof Error ? err.message : t('failed'));
    } finally {
      if (mounted.current) setSaving(false);
    }
  };

  if (!status) return loadError ? <Alert type="error" title={loadError} showIcon /> : <Spin />;

  return (
    <div className="space-y-6">
      <p className="text-fg-muted m-0 text-sm">{t('description')}</p>
      {saveError && <Alert type="error" title={saveError} showIcon />}

      <SettingsSection
        title={t('channels')}
        actions={
          <Dropdown
            trigger={['click']}
            menu={{
              items: status.types.map((type) => ({ key: type.type, label: typeName(type.type) })),
              onClick: ({ key }) => add(key)
            }}
          >
            <Button size="small" icon={<PlusIcon size={14} />}>
              {t('add')}
            </Button>
          </Dropdown>
        }
      >
        {channels.length === 0 && <p className="text-fg-muted m-0 text-sm">{t('empty')}</p>}
        {channels.map((channel) => (
          <ChannelPanel
            key={channel.key}
            channel={channel}
            type={status.types.find((type) => type.type === channel.type)}
            events={status.events}
            typeName={typeName(channel.type)}
            onChange={(patch) => change(channel.key, patch)}
            onRemove={() => remove(channel.key)}
          />
        ))}
      </SettingsSection>

      <SettingsSection title={t('messages')}>
        <SettingRow label={t('language')} htmlFor="notify-language">
          <Select
            id="notify-language"
            className="w-40"
            value={language}
            options={status.languages.map((value) => ({
              value,
              label: languageNames[value] ?? value
            }))}
            onChange={(value) => {
              setLanguage(value);
              setDirty(true);
            }}
          />
        </SettingRow>
      </SettingsSection>

      <Button type="primary" loading={saving} disabled={!dirty} onClick={() => void save()}>
        {t('save')}
      </Button>
    </div>
  );
};

type ChannelPanelProps = {
  channel: Draft;
  type?: NotifyChannelType;
  events: NotifyKind[];
  typeName: string;
  onChange: (patch: Partial<Draft>) => void;
  onRemove: () => void;
};

const ChannelPanel = ({
  channel,
  type,
  events,
  typeName,
  onChange,
  onRemove
}: ChannelPanelProps) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.notifications' });
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; text: string }>();
  const id = `notify-${channel.key}`;

  const test = async () => {
    setTesting(true);
    setTestResult(undefined);
    try {
      const response = await testNotifyChannel(update(channel));
      setTestResult(
        response.code === 0
          ? { ok: true, text: t('testSent') }
          : { ok: false, text: t('testFailed', { error: response.msg }) }
      );
    } catch (err) {
      setTestResult({
        ok: false,
        text: t('testFailed', { error: err instanceof Error ? err.message : '' })
      });
    } finally {
      setTesting(false);
    }
  };

  const setting = (key: string, value: string) =>
    onChange({
      settings: { ...channel.settings, [key]: value },
      clear: channel.clear.filter((item) => item !== key)
    });

  return (
    <Panel flush className="overflow-hidden">
      <div className="flex items-center gap-3 px-4 py-3">
        <button
          type="button"
          className="nanokvm-button-base flex min-w-0 flex-auto items-center gap-2 text-left"
          aria-expanded={channel.open}
          aria-controls={`${id}-body`}
          onClick={() => onChange({ open: !channel.open })}
        >
          <ChevronDownIcon
            size={16}
            className={
              channel.open
                ? 'shrink-0 rotate-180 transition-transform'
                : 'shrink-0 transition-transform'
            }
          />
          <span className="text-fg truncate">{channel.name || typeName}</span>
          {channel.name && <span className="text-fg-muted truncate text-xs">{typeName}</span>}
        </button>
        <IconButton
          label={t('test')}
          icon={<SendIcon size={16} />}
          loading={testing}
          onClick={() => void test()}
        />
        <Popconfirm title={t('removeConfirm')} onConfirm={onRemove}>
          <IconButton label={t('remove')} icon={<Trash2Icon size={16} />} />
        </Popconfirm>
        <Switch
          size="small"
          aria-label={t('enabled')}
          checked={channel.enabled}
          onChange={(enabled) => onChange({ enabled })}
        />
      </div>
      {testResult && (
        <div className="px-4 pb-3">
          <Alert type={testResult.ok ? 'success' : 'error'} title={testResult.text} showIcon />
        </div>
      )}
      <div id={`${id}-body`} hidden={!channel.open} className="space-y-4 px-4 pb-4">
        <p className="text-fg-muted m-0 text-xs">{t(`help.${channel.type}`)}</p>
        <SettingRow label={t('name')} htmlFor={`${id}-name`} stacked>
          <Input
            id={`${id}-name`}
            maxLength={64}
            placeholder={typeName}
            value={channel.name}
            onChange={(event) => onChange({ name: event.target.value })}
          />
        </SettingRow>
        <SettingRow label={t('events')} stacked>
          <Checkbox.Group
            value={channel.events}
            options={events.map((kind) => ({ value: kind, label: t(`event.${kind}`) }))}
            onChange={(value) => onChange({ events: value as NotifyKind[] })}
          />
          <ul className="text-fg-muted mt-2 mb-0 list-none space-y-0.5 p-0 text-xs">
            {events.map((kind) => (
              <li key={kind}>
                {t(`event.${kind}`)}: {t(`eventHint.${kind}`)}
              </li>
            ))}
          </ul>
        </SettingRow>
        {type?.fields.map((field) => (
          <FieldInput
            key={field.key}
            id={`${id}-${field.key}`}
            field={field}
            value={channel.settings[field.key] ?? ''}
            stored={channel.secretsSet.includes(field.key) && !channel.clear.includes(field.key)}
            onChange={(value) => setting(field.key, value)}
            onClear={() =>
              onChange({
                clear: [...channel.clear, field.key],
                settings: { ...channel.settings, [field.key]: '' }
              })
            }
          />
        ))}
      </div>
    </Panel>
  );
};

type FieldInputProps = {
  id: string;
  field: NotifyField;
  value: string;
  // A secret is stored: an empty field keeps it.
  stored: boolean;
  onChange: (value: string) => void;
  onClear: () => void;
};

const FieldInput = ({ id, field, value, stored, onChange, onClear }: FieldInputProps) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.notifications' });
  const label = (
    <>
      {t(`field.${field.key}`)}
      {field.required && <span className="text-danger"> *</span>}
    </>
  );

  if (field.kind === 'bool') {
    return (
      <SettingRow label={label} htmlFor={id}>
        <Switch
          id={id}
          size="small"
          checked={(value || field.default) === 'true'}
          onChange={(on) => onChange(on ? 'true' : 'false')}
        />
      </SettingRow>
    );
  }
  if (field.kind === 'select') {
    return (
      <SettingRow label={label} htmlFor={id}>
        <Select
          id={id}
          className="w-40"
          value={value || field.default}
          options={field.options?.map((option) => ({
            value: option,
            label: t(`option.${option}`)
          }))}
          onChange={onChange}
        />
      </SettingRow>
    );
  }

  const placeholder = stored ? t('secretKept') : (field.placeholder ?? field.default);
  const input = field.secret ? (
    <Input.Password
      id={id}
      autoComplete="new-password"
      placeholder={placeholder}
      value={value}
      onChange={(event) => onChange(event.target.value)}
    />
  ) : (
    <Input
      id={id}
      type={field.kind === 'number' ? 'number' : field.kind === 'email' ? 'email' : 'text'}
      inputMode={field.kind === 'number' ? 'numeric' : undefined}
      placeholder={placeholder}
      value={value}
      onChange={(event) => onChange(event.target.value)}
    />
  );
  return (
    <SettingRow label={label} htmlFor={id} stacked>
      <div className="flex items-center gap-2">
        {input}
        {stored && !field.required && (
          <Button size="small" onClick={onClear}>
            {t('secretClear')}
          </Button>
        )}
      </div>
    </SettingRow>
  );
};
