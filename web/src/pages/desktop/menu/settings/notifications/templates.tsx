import { useEffect, useState } from 'react';
import { Alert, Button, Input, Popconfirm, Spin } from 'antd';
import { ChevronDownIcon, EyeIcon, PlusIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  previewNotify,
  type NotifyMessage,
  type NotifyMessageView,
  type NotifyPreview,
  type NotifyTemplate
} from '@/api/notify.ts';
import { IconButton, Panel, SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

import { templateOf, type TemplateDraft } from './template.ts';

// i18next nests keys at dots; a message key has them.
const messageLabelKey = (key: string) => `message.${key.replace(/\./g, '_')}`;

type TemplatesProps = {
  templates: TemplateDraft[];
  messages: NotifyMessageView[];
  commonVars: string[];
  language: string;
  onAdd: () => void;
  onChange: (id: string, patch: Partial<TemplateDraft>) => void;
  onRemove: (id: string) => void;
};

// The templates section: each template sets the title and text of the
// messages it wants; the others are the standard ones of the language.
export const Templates = ({
  templates,
  messages,
  commonVars,
  language,
  onAdd,
  onChange,
  onRemove
}: TemplatesProps) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.notifications' });
  return (
    <SettingsSection
      title={t('templates')}
      actions={
        <Button size="small" icon={<PlusIcon size={14} />} onClick={onAdd}>
          {t('addTemplate')}
        </Button>
      }
    >
      <p className="text-fg-muted m-0 text-sm">{t('templatesDescription')}</p>
      {templates.length === 0 && <p className="text-fg-muted m-0 text-sm">{t('templatesEmpty')}</p>}
      {templates.map((template) => (
        <TemplatePanel
          key={template.id}
          template={template}
          messages={messages}
          commonVars={commonVars}
          language={language}
          onChange={(patch) => onChange(template.id, patch)}
          onRemove={() => onRemove(template.id)}
        />
      ))}
    </SettingsSection>
  );
};

type TemplatePanelProps = {
  template: TemplateDraft;
  messages: NotifyMessageView[];
  commonVars: string[];
  language: string;
  onChange: (patch: Partial<TemplateDraft>) => void;
  onRemove: () => void;
};

const TemplatePanel = ({
  template,
  messages,
  commonVars,
  language,
  onChange,
  onRemove
}: TemplatePanelProps) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.notifications' });
  const id = `notify-template-${template.id}`;
  const [previewOpen, setPreviewOpen] = useState(false);

  const setMessage = (key: string, patch: Partial<NotifyMessage>) => {
    const current = template.messages[key] ?? { title: '', body: '' };
    onChange({ messages: { ...template.messages, [key]: { ...current, ...patch } } });
  };

  return (
    <Panel flush className="overflow-hidden">
      <div className="flex items-center gap-3 px-4 py-3">
        <button
          type="button"
          className="nanokvm-button-base flex min-w-0 flex-auto items-center gap-2 text-left"
          aria-expanded={template.open}
          aria-controls={`${id}-body`}
          onClick={() => onChange({ open: !template.open })}
        >
          <ChevronDownIcon
            size={16}
            className={
              template.open
                ? 'shrink-0 rotate-180 transition-transform'
                : 'shrink-0 transition-transform'
            }
          />
          <span className="text-fg truncate">{template.name || t('untitled')}</span>
        </button>
        <IconButton
          label={t('preview')}
          icon={<EyeIcon size={16} />}
          onClick={() => setPreviewOpen((open) => !open)}
        />
        <Popconfirm title={t('removeTemplateConfirm')} onConfirm={onRemove}>
          <IconButton label={t('removeTemplate')} icon={<Trash2Icon size={16} />} />
        </Popconfirm>
      </div>
      {previewOpen && (
        <div className="px-4 pb-3">
          <Previews language={language} template={templateOf(template)} />
        </div>
      )}
      <div id={`${id}-body`} hidden={!template.open} className="space-y-4 px-4 pb-4">
        <SettingRow label={t('templateName')} htmlFor={`${id}-name`} stacked>
          <Input
            id={`${id}-name`}
            maxLength={64}
            value={template.name}
            onChange={(event) => onChange({ name: event.target.value })}
          />
        </SettingRow>
        <p className="text-fg-muted m-0 text-xs">{t('templateHelp')}</p>
        {messages.map((m) => {
          const own = template.messages[m.key] ?? { title: '', body: '' };
          const vars = [...(m.vars ?? []), ...commonVars].map((name) => `{${name}}`).join(' ');
          return (
            <fieldset key={m.key} className="m-0 space-y-2 border-0 p-0">
              <legend className="text-fg mb-1 p-0 text-sm font-medium">
                {t(messageLabelKey(m.key))}
              </legend>
              <Input
                id={`${id}-${m.key}-title`}
                aria-label={`${t(messageLabelKey(m.key))}: ${t('messageTitle')}`}
                maxLength={256}
                placeholder={m.title}
                value={own.title}
                onChange={(event) => setMessage(m.key, { title: event.target.value })}
              />
              <Input.TextArea
                id={`${id}-${m.key}-body`}
                aria-label={`${t(messageLabelKey(m.key))}: ${t('messageBody')}`}
                autoSize={{ minRows: 2, maxRows: 8 }}
                maxLength={2048}
                placeholder={m.body}
                value={own.body}
                onChange={(event) => setMessage(m.key, { body: event.target.value })}
              />
              <p className="text-fg-muted m-0 text-xs">{t('variables', { vars })}</p>
            </fieldset>
          );
        })}
      </div>
    </Panel>
  );
};

type PreviewsProps = {
  language: string;
  // null: the standard messages.
  template: NotifyTemplate | null;
};

// Every message as a channel gets it, with sample values, from the server
// (it writes them the way it sends them).
export const Previews = ({ language, template }: PreviewsProps) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.notifications' });
  // The answer is kept with the request it answers: an older one is not shown.
  const [result, setResult] = useState<{
    request: string;
    previews?: NotifyPreview[];
    error?: string;
  }>();
  const request = JSON.stringify({ language, template });

  useEffect(() => {
    let current = true;
    // Typing in a template asks once it pauses.
    const timer = setTimeout(() => {
      previewNotify(JSON.parse(request) as PreviewsProps)
        .then((response) => {
          if (response.code !== 0) throw new Error(response.msg);
          if (current) setResult({ request, previews: response.data });
        })
        .catch((err: unknown) => {
          if (current)
            setResult({ request, error: err instanceof Error ? err.message : String(err) });
        });
    }, 300);
    return () => {
      current = false;
      clearTimeout(timer);
    };
  }, [request]);

  if (result?.request !== request) return <Spin size="small" />;
  if (result.error)
    return <Alert type="error" title={t('previewFailed', { error: result.error })} showIcon />;
  return (
    <div className="space-y-3">
      <p className="text-fg-muted m-0 text-xs">{t('previewNote')}</p>
      {result.previews?.map((p) => (
        <div key={p.key} className="border-border rounded border border-solid px-3 py-2">
          <div className="text-fg-muted text-xs">{t(messageLabelKey(p.key))}</div>
          <div className="text-fg text-sm font-medium">{p.title}</div>
          <div className="text-fg m-0 text-sm whitespace-pre-wrap">{p.text}</div>
        </div>
      ))}
    </div>
  );
};
