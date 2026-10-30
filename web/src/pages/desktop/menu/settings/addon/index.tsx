import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { Alert, Button, Form, Input, InputNumber, message, Select, Spin, Switch } from 'antd';
import { isAxiosError } from 'axios';
import { useTranslation } from 'react-i18next';

import type { AddonField, AddonForm, AddonManifest } from '@/api/addons.ts';
import { formValues, localized, statusText, submitValues } from '@/lib/addon-form.ts';
import { http } from '@/lib/http.ts';
import { requestErrorText } from '@/lib/request-error.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { SettingsSection } from '@/components/ui/settings.tsx';

// The settings page of an add-on, drawn from the form in its manifest
// (docs/ADDONS.md). Values come from the add-on's own API through the
// server's proxy; core keeps the look, the add-on only describes the fields.
// embedded: one section of a core page (settings.section) instead of a page.
type AddonPageProps = { manifest: AddonManifest; embedded?: boolean };

export const AddonPage = (props: AddonPageProps) => (
  // A changed definition gets a new form instance. Pending requests from
  // the previous definition must not write values into its replacement.
  <AddonFormPage key={JSON.stringify(props.manifest.settings!.form)} {...props} />
);

const AddonFormPage = ({ manifest, embedded = false }: AddonPageProps) => {
  const { t, i18n } = useTranslation('translation', { keyPrefix: 'settings.addonPage' });
  // The inventory poll brings a new manifest object every time: start over
  // (and reset an edited form) only when the form itself changed.
  const formJson = JSON.stringify(manifest.settings!.form);
  const form = useMemo(() => JSON.parse(formJson) as AddonForm, [formJson]);
  const text = useCallback(
    (value: Record<string, string> | undefined) => localized(value, i18n.language),
    [i18n.language]
  );
  const [data, setData] = useState<Record<string, unknown>>();
  // Reading the values and the last save or action fail separately: a
  // successful reload must not hide why a save was refused.
  const [loadError, setLoadError] = useState('');
  const [actionError, setActionError] = useState('');
  // The server could not reach the socket (HTTP 502): the add-on's service
  // is stopped, which is a state (IPMI switched off), not an error.
  const [stopped, setStopped] = useState(false);
  const [busy, setBusy] = useState('');
  const [values] = Form.useForm();
  const dirty = useRef(false);
  const mounted = useRef(false);
  const editRevision = useRef(0);
  const readRevision = useRef(0);
  const pendingRead = useRef<number | null>(null);
  const posting = useRef(false);
  const invalidateReads = useCallback(() => {
    readRevision.current++;
    pendingRead.current = null;
  }, []);

  const refresh = useCallback(async () => {
    // A slow source must get to finish instead of being superseded by each
    // poll tick. Mutations and form teardown explicitly invalidate that read.
    if (!mounted.current || posting.current || pendingRead.current !== null) return;
    const revision = ++readRevision.current;
    pendingRead.current = revision;
    try {
      const response = await http.get(form.source);
      if (!mounted.current || revision !== readRevision.current) return;
      if (response.code !== 0) throw response;
      const next = (response.data ?? {}) as Record<string, unknown>;
      setData(next);
      if (!dirty.current) values.setFieldsValue(formValues(form.fields, next));
      setLoadError('');
      setStopped(false);
    } catch (err) {
      if (!mounted.current || revision !== readRevision.current) return;
      const notRunning = isAxiosError(err) && err.response?.status === 502;
      setStopped(notRunning);
      setLoadError(notRunning ? '' : (requestErrorText(err, t('failed')) ?? ''));
    } finally {
      if (pendingRead.current === revision) pendingRead.current = null;
    }
  }, [form, values, t]);

  useEffect(() => {
    mounted.current = true;
    void refresh();
    const stop = form.refresh
      ? pollWhileVisible(() => void refresh(), form.refresh * 1000)
      : undefined;
    return () => {
      mounted.current = false;
      invalidateReads();
      stop?.();
    };
  }, [form, refresh, invalidateReads]);

  const post = async (
    key: string,
    url: string,
    body?: Record<string, unknown>,
    onSuccess?: () => void
  ) => {
    if (!mounted.current || posting.current) return;
    posting.current = true;
    // A read started before this mutation can no longer describe its result.
    invalidateReads();
    setBusy(key);
    try {
      const response = await http.post(url, body, { sensitive: true });
      if (!mounted.current) return;
      if (response.code !== 0) throw response;
      setActionError('');
      onSuccess?.();
    } catch (err) {
      if (mounted.current) setActionError(requestErrorText(err, t('refused')) ?? '');
    } finally {
      posting.current = false;
      if (mounted.current) {
        setBusy('');
        void refresh();
      }
    }
  };

  const save = async (edited: Record<string, unknown>) => {
    if (!form.submit) return;
    const revision = editRevision.current;
    await post('submit', form.submit, submitValues(form.fields, edited), () => {
      // A successful POST only saved the submitted revision. Keep any edits
      // made while it was in flight when the following GET refreshes status.
      if (revision === editRevision.current) dirty.current = false;
      void message.success(t('saved'));
    });
  };

  const section = (children: ReactNode) =>
    embedded ? (
      <SettingsSection title={text(manifest.title)} description={text(manifest.description)}>
        {children}
      </SettingsSection>
    ) : (
      children
    );
  const stoppedNote = <p className="text-fg-muted m-0 text-sm">{t('notRunning')}</p>;
  // Values of a stopped service would be stale: the note alone.
  if (!data || stopped)
    return section(
      stopped ? (
        stoppedNote
      ) : loadError ? (
        <Alert type="error" title={loadError} showIcon />
      ) : (
        <Spin size="small" />
      )
    );

  const control = (field: AddonField) => {
    switch (field.type) {
      case 'password':
        return <Input.Password autoComplete="new-password" />;
      case 'number':
        return <InputNumber min={field.min} max={field.max} />;
      case 'select':
        return (
          <Select
            options={field.options?.map((option) => ({
              value: option.value,
              label: text(option.label)
            }))}
          />
        );
      case 'switch':
        return <Switch />;
      default:
        return <Input />;
    }
  };

  const status = form.fields.filter((field) => field.type === 'status');
  const editable = form.fields.filter((field) => field.type !== 'status');

  const statusList = (
    <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
      {status.map((field) => (
        <div key={field.key} className="contents">
          <dt className="text-fg-muted">{text(field.label)}</dt>
          <dd className="m-0 break-words">{statusText(data[field.key], t('yes'), t('no'))}</dd>
        </div>
      ))}
    </dl>
  );

  return section(
    <div className={embedded ? 'space-y-4' : 'space-y-6'}>
      {!embedded && manifest.description && (
        <p className="text-fg-muted m-0 text-sm">{text(manifest.description)}</p>
      )}
      {loadError && <Alert type="error" title={loadError} showIcon />}
      {actionError && <Alert type="error" title={actionError} showIcon />}
      {status.length > 0 &&
        (embedded ? (
          statusList
        ) : (
          <SettingsSection title={t('status')}>{statusList}</SettingsSection>
        ))}
      {editable.length > 0 && (
        <Form
          form={values}
          layout="vertical"
          onValuesChange={() => {
            dirty.current = true;
            editRevision.current++;
          }}
          onFinish={(edited) => void save(edited)}
        >
          {editable.map((field) => (
            <Form.Item
              key={field.key}
              name={field.key}
              label={text(field.label)}
              extra={field.help && text(field.help)}
              valuePropName={field.type === 'switch' ? 'checked' : 'value'}
              rules={field.required && field.type !== 'password' ? [{ required: true }] : []}
            >
              {control(field)}
            </Form.Item>
          ))}
          <Button
            type="primary"
            htmlType="submit"
            loading={busy === 'submit'}
            disabled={busy !== '' && busy !== 'submit'}
          >
            {t('save')}
          </Button>
        </Form>
      )}
      {form.actions && form.actions.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {form.actions.map((action) => (
            <Button
              key={action.id}
              danger={action.danger}
              loading={busy === `action:${action.id}`}
              disabled={busy !== '' && busy !== `action:${action.id}`}
              onClick={async () => {
                if (action.confirm) {
                  const confirmed = await confirmAction({
                    title: text(action.label),
                    content: text(action.confirm),
                    danger: action.danger
                  });
                  if (!confirmed) return;
                }
                void post(`action:${action.id}`, action.url);
              }}
            >
              {text(action.label)}
            </Button>
          ))}
        </div>
      )}
    </div>
  );
};
