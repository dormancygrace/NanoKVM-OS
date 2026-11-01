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
import { ChevronDownIcon, EyeIcon, PlusIcon, SendIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  getNotifyConfig,
  saveNotifyConfig,
  testNotifyChannel,
  type NotifyChannelType,
  type NotifyChannelUpdate,
  type NotifyField,
  type NotifyKind,
  type NotifyProvider,
  type NotifyStatus
} from '@/api/notify.ts';
import { IconButton, Panel, SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

import { ProviderSelect, SignIn } from './email.tsx';
import { newTemplateId, templateOf, type TemplateDraft } from './template.ts';
import { Previews, Templates } from './templates.tsx';

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

// Each language by its own name, as the language menu of the interface.
const languageNames: Record<string, string> = {
  en: 'English',
  ca: 'Català',
  cz: 'Čeština',
  da: 'Dansk',
  de: 'Deutsch',
  es: 'Español',
  fr: 'Français',
  he: 'עברית',
  hu: 'Magyar',
  id: 'Bahasa Indonesia',
  it: 'Italiano',
  ja: '日本語',
  ko: '한국어',
  nb: 'Norsk bokmål',
  nl: 'Nederlands',
  pl: 'Polski',
  pt_br: 'Português (Brasil)',
  ru: 'Русский',
  se: 'Svenska',
  th: 'ไทย',
  tr: 'Türkçe',
  uk: 'Українська',
  vi: 'Tiếng Việt',
  zh: '简体中文',
  zh_tw: '繁體中文'
};

const templateDraftsOf = (status: NotifyStatus, open = new Map<string, boolean>()) =>
  status.templates.map((template) => ({
    ...template,
    messages: { ...template.messages },
    open: open.get(template.id) ?? false
  }));

// A field applies while the fields it names have one of the values, their
// defaults counted.
const shown = (field: NotifyField, fields: NotifyField[], settings: Record<string, string>) =>
  Object.entries(field.show ?? {}).every(([key, values]) =>
    values.includes(settings[key] || fields.find((f) => f.key === key)?.default || '')
  );

const provider = (providers: NotifyProvider[], channel: NotifyChannelUpdate) =>
  providers.find((p) => p.id === (channel.settings.provider || 'custom'));

type Draft = NotifyChannelUpdate & {
  secretsSet: string[];
  authError?: string;
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
  clear: draft.clear,
  template: draft.template ?? ''
});

// Services > Notifications: channels that get messages about the host and
// the device. Each channel picks its events; Test sends through a channel as
// it is on the page, Save keeps the whole list.
export const Notifications = () => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.notifications' });
  const [status, setStatus] = useState<NotifyStatus>();
  const [channels, setChannels] = useState<Draft[]>([]);
  const [templates, setTemplates] = useState<TemplateDraft[]>([]);
  const [language, setLanguage] = useState('en');
  const [standardPreview, setStandardPreview] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [saveError, setSaveError] = useState('');
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);
  const mounted = useRef(false);
  // revision counts the edits; a save that returns after a newer edit must
  // not replace it.
  const revision = useRef(0);
  const edited = () => {
    revision.current++;
    setDirty(true);
  };

  const show = useCallback((next: NotifyStatus) => {
    setStatus(next);
    setChannels(draftOf(next));
    setTemplates(templateDraftsOf(next));
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

  // A patch made from the channel as it is now (a function) keeps edits made
  // since the caller last rendered, such as during a sign-in.
  const change = (key: string, patch: Partial<Draft> | ((item: Draft) => Partial<Draft>)) => {
    setChannels((list) =>
      list.map((item) =>
        item.key === key
          ? { ...item, ...(typeof patch === 'function' ? patch(item) : patch) }
          : item
      )
    );
    // Folding a channel is not an edit.
    if (typeof patch === 'function' || Object.keys(patch).some((name) => name !== 'open')) {
      edited();
    }
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
    edited();
  };

  const remove = (key: string) => {
    setChannels((list) => list.filter((item) => item.key !== key));
    edited();
  };

  const addTemplate = () => {
    setTemplates((list) => [...list, { id: newTemplateId(), name: '', messages: {}, open: true }]);
    edited();
  };

  const changeTemplate = (id: string, patch: Partial<TemplateDraft>) => {
    setTemplates((list) => list.map((item) => (item.id === id ? { ...item, ...patch } : item)));
    if (Object.keys(patch).some((name) => name !== 'open')) edited();
  };

  // Channels that used a deleted template get the standard messages.
  const removeTemplate = (id: string) => {
    setTemplates((list) => list.filter((item) => item.id !== id));
    setChannels((list) =>
      list.map((item) => (item.template === id ? { ...item, template: '' } : item))
    );
    edited();
  };

  const save = async () => {
    setSaving(true);
    const submitted = channels;
    const submittedRevision = revision.current;
    try {
      const response = await saveNotifyConfig({
        language,
        channels: submitted.map(update),
        templates: templates.map(templateOf)
      });
      if (!mounted.current) return;
      if (response.code !== 0) throw new Error(response.msg);
      const saved = response.data;
      if (revision.current === submittedRevision) {
        // Nothing changed meanwhile: show what the device keeps, with the
        // folds as they are (the server keeps the order of the list).
        setStatus(saved);
        setChannels((list) => {
          const open = new Map(list.map((item) => [item.key, item.open]));
          return draftOf(saved).map((item, i) => ({
            ...item,
            open: open.get(submitted[i]?.key ?? '') ?? false
          }));
        });
        setTemplates((list) =>
          templateDraftsOf(saved, new Map(list.map((item) => [item.id, item.open])))
        );
        setLanguage(saved.language);
        setDirty(false);
      } else {
        // Templates keep the ids the page gave them: the drafts stay as they are.
        // Edits made while saving stay, still to be saved. The channels sent
        // take the ids and stored secrets the device gave them, so their
        // secrets are kept by the next save too.
        setStatus(saved);
        setChannels((list) =>
          list.map((item) => {
            const i = submitted.findIndex((sent) => sent.key === item.key);
            const stored = i >= 0 ? saved.channels[i] : undefined;
            return stored ? { ...item, id: stored.id, secretsSet: stored.secretsSet } : item;
          })
        );
      }
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
            templates={templates}
            providers={status.providers}
            onChange={(patch) => change(channel.key, patch)}
            onRemove={() => remove(channel.key)}
          />
        ))}
      </SettingsSection>

      <SettingsSection title={t('messages')}>
        <SettingRow label={t('language')} htmlFor="notify-language">
          <Select
            id="notify-language"
            className="w-48"
            showSearch={{ optionFilterProp: 'label' }}
            value={language}
            options={status.languages.map((value) => ({
              value,
              label: languageNames[value] ?? value
            }))}
            onChange={(value) => {
              setLanguage(value);
              edited();
            }}
          />
        </SettingRow>
        <p className="text-fg-muted m-0 text-xs">{t('languageHelp')}</p>
        <div>
          <Button
            size="small"
            icon={<EyeIcon size={14} />}
            aria-expanded={standardPreview}
            onClick={() => setStandardPreview((open) => !open)}
          >
            {t('previewStandard')}
          </Button>
        </div>
        {standardPreview && <Previews language={language} template={null} />}
      </SettingsSection>

      <Templates
        templates={templates}
        messages={status.messages}
        commonVars={status.commonVars}
        language={language}
        onAdd={addTemplate}
        onChange={changeTemplate}
        onRemove={removeTemplate}
      />

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
  templates: TemplateDraft[];
  providers: NotifyProvider[];
  onChange: (patch: Partial<Draft> | ((item: Draft) => Partial<Draft>)) => void;
  onRemove: () => void;
};

const ChannelPanel = ({
  channel,
  type,
  events,
  typeName,
  templates,
  providers,
  onChange,
  onRemove
}: ChannelPanelProps) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.notifications' });
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; text: string }>();
  const id = `notify-${channel.key}`;
  const template = templates.find((item) => item.id === channel.template);

  const test = async () => {
    setTesting(true);
    setTestResult(undefined);
    try {
      // The test message is written by the template as it is on the page.
      const response = await testNotifyChannel({
        ...update(channel),
        templateDraft: template && templateOf(template)
      });
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
      {channel.authError && !testResult && (
        <div className="px-4 pb-3">
          <Alert
            type="warning"
            title={t('oauth.authError', { error: channel.authError })}
            showIcon
          />
        </div>
      )}
      <div id={`${id}-body`} hidden={!channel.open} className="space-y-4 px-4 pb-4">
        {/* The e-mail channel's help is its provider's, under the picker. */}
        {channel.type !== 'email' && (
          <p className="text-fg-muted m-0 text-xs">{t(`help.${channel.type}`)}</p>
        )}
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
        <SettingRow label={t('template')} htmlFor={`${id}-template`}>
          <Select
            id={`${id}-template`}
            className="w-48"
            value={template ? template.id : ''}
            options={[
              { value: '', label: t('standardTemplate') },
              ...templates.map((item) => ({ value: item.id, label: item.name || t('untitled') }))
            ]}
            onChange={(value) => onChange({ template: value })}
          />
        </SettingRow>
        {type?.fields
          .filter((field) => field.kind !== 'hidden' && shown(field, type.fields, channel.settings))
          // The sign-ins of the e-mail channel are the provider's; with one
          // there is nothing to choose.
          .map((field) =>
            field.key === 'auth' && channel.type === 'email'
              ? { ...field, options: provider(providers, channel)?.auth ?? field.options }
              : field
          )
          .filter((field) => field.key !== 'auth' || (field.options?.length ?? 0) > 1)
          .map((field) => {
            const stored =
              channel.secretsSet.includes(field.key) && !channel.clear.includes(field.key);
            if (field.key === 'provider' && channel.type === 'email') {
              return (
                <ProviderSelect
                  key={field.key}
                  id={`${id}-${field.key}`}
                  providers={providers}
                  settings={channel.settings}
                  onChange={(settings) => onChange({ settings })}
                />
              );
            }
            if (field.kind === 'oauth') {
              return (
                <SignIn
                  key={field.key}
                  channel={update(channel)}
                  provider={provider(providers, channel)}
                  stored={stored}
                  onSignedIn={(token, account) =>
                    onChange((item) => ({
                      settings: {
                        ...item.settings,
                        [field.key]: token,
                        oauthAccount: account,
                        username: item.settings.username || account,
                        from: item.settings.from || account
                      },
                      clear: item.clear.filter((key) => key !== field.key)
                    }))
                  }
                  onSignOut={() =>
                    onChange((item) => ({
                      clear: [...item.clear.filter((key) => key !== field.key), field.key],
                      settings: { ...item.settings, [field.key]: '', oauthAccount: '' }
                    }))
                  }
                />
              );
            }
            return (
              <FieldInput
                key={field.key}
                id={`${id}-${field.key}`}
                field={field}
                value={channel.settings[field.key] ?? ''}
                stored={stored}
                onChange={(value) => setting(field.key, value)}
                onClear={() =>
                  onChange({
                    clear: [...channel.clear, field.key],
                    settings: { ...channel.settings, [field.key]: '' }
                  })
                }
              />
            );
          })}
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
