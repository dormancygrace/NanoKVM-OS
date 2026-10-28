import { useEffect, useState } from 'react';
import { Alert, Button, Input, message, Modal, Switch } from 'antd';
import { useAtom } from 'jotai';
import { BookOpenIcon, CpuIcon, KeyRoundIcon, LinkIcon, MessageSquareIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getRuntimeStatus, setPicoclawHostTools, setPicoclawModelConfig } from '@/api/picoclaw.ts';
import { modelConfigComplete, modelProviderAllowsEmptyApiKey } from '@/lib/picoclaw-model.ts';
import { picoclawRuntimeStatusAtom, type PicoclawRuntimeStatus } from '@/jotai/picoclaw.ts';
import { Panel, SettingRow, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

const replyText = (rsp: { msg?: string; message?: string }) => rsp.message || rsp.msg || '';

// Settings > AI > PicoClaw: the model the agent talks to and its tools on the
// NanoKVM, the same settings as the chat panel's menu. The server never sends
// the API key or base URL back: saving takes all three again.
export const PicoclawSettingsPage = ({
  onOpenAddons,
  onOpenChat
}: {
  onOpenAddons: () => void;
  onOpenChat: () => void;
}) => {
  const { t } = useTranslation();
  const [status, setStatus] = useAtom(picoclawRuntimeStatusAtom);
  const [model, setModel] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [apiBase, setApiBase] = useState('');
  const [saving, setSaving] = useState(false);
  const [savingTools, setSavingTools] = useState(false);

  useEffect(() => {
    let active = true;
    getRuntimeStatus()
      .then((rsp) => {
        if (active && rsp.code === 0) setStatus(rsp.data as PicoclawRuntimeStatus);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [setStatus]);

  async function saveModel() {
    if (!modelConfigComplete(model, apiBase, apiKey)) {
      message.error(
        t(
          modelProviderAllowsEmptyApiKey(model)
            ? 'picoclaw.model.invalidNoKey'
            : 'picoclaw.model.invalid'
        )
      );
      return;
    }
    setSaving(true);
    try {
      const rsp = await setPicoclawModelConfig({
        model: model.trim(),
        api_base: apiBase.trim(),
        api_key: apiKey.trim()
      });
      if (rsp.code !== 0) throw new Error(replyText(rsp) || t('picoclaw.model.saveFailed'));
      if (rsp.data?.status) setStatus(rsp.data.status as PicoclawRuntimeStatus);
      setApiKey('');
      message.success(t('picoclaw.model.saved'));
    } catch (error) {
      message.error(error instanceof Error ? error.message : t('picoclaw.model.saveFailed'));
    } finally {
      setSaving(false);
    }
  }

  async function changeTools(enabled: boolean) {
    if (enabled) {
      const confirmed = await new Promise<boolean>((resolve) =>
        Modal.confirm({
          title: t('picoclaw.model.hostTools'),
          content: t('picoclaw.model.hostToolsWarning'),
          okText: t('picoclaw.model.hostToolsEnable'),
          okButtonProps: { danger: true },
          cancelText: t('picoclaw.cancel'),
          onOk: () => resolve(true),
          onCancel: () => resolve(false)
        })
      );
      if (!confirmed) return;
    }
    setSavingTools(true);
    try {
      const rsp = await setPicoclawHostTools(enabled);
      if (rsp.code !== 0) throw new Error(replyText(rsp) || t('picoclaw.model.hostToolsFailed'));
      if (rsp.data?.status) setStatus(rsp.data.status as PicoclawRuntimeStatus);
    } catch (error) {
      message.error(error instanceof Error ? error.message : t('picoclaw.model.hostToolsFailed'));
    } finally {
      setSavingTools(false);
    }
  }

  if (status && !status.installed) {
    return (
      <Alert
        type="info"
        showIcon
        message={t('settings.ai.picoclaw.notInstalled')}
        action={
          <Button size="small" onClick={onOpenAddons}>
            {t('settings.ai.screenText.openAddons')}
          </Button>
        }
      />
    );
  }

  const hostTools = status?.runtime_intent?.host_tools ?? false;
  const keyOptional = modelProviderAllowsEmptyApiKey(model);
  return (
    <div className="space-y-6">
      <Panel className="space-y-4">
        <SettingRow
          label={t('settings.ai.picoclaw.model')}
          description={
            status?.model_configured ? (
              <StatusBadge tone="success">{status.model_name || '—'}</StatusBadge>
            ) : (
              <StatusBadge tone="warning">{t('settings.ai.picoclaw.noModel')}</StatusBadge>
            )
          }
        >
          <Button
            icon={<MessageSquareIcon size={14} />}
            disabled={!status?.model_configured}
            onClick={onOpenChat}
          >
            {t('settings.ai.picoclaw.openChat')}
          </Button>
        </SettingRow>
      </Panel>

      <SettingsSection
        title={t('settings.ai.picoclaw.changeModel')}
        description={t('settings.ai.picoclaw.changeModelDescription')}
        actions={
          <Button
            type="link"
            size="small"
            icon={<BookOpenIcon size={13} />}
            href="https://docs.picoclaw.io/docs/configuration/model-list/#supported-vendors-and-protocols"
            target="_blank"
            rel="noreferrer"
          >
            {t('picoclaw.model.docsTitle')}
          </Button>
        }
      >
        <Panel className="space-y-4">
          <SettingRow label={t('picoclaw.model.modelIdentifier')} htmlFor="picoclaw-model" stacked>
            <Input
              id="picoclaw-model"
              prefix={<CpuIcon size={13} />}
              placeholder={status?.model_name || t('picoclaw.model.modelIdentifierPlaceholder')}
              value={model}
              onChange={(e) => setModel(e.target.value)}
            />
          </SettingRow>
          <SettingRow label={t('picoclaw.model.apiKey')} htmlFor="picoclaw-key" stacked>
            <Input.Password
              id="picoclaw-key"
              prefix={<KeyRoundIcon size={13} />}
              autoComplete="off"
              placeholder={t(
                keyOptional
                  ? 'picoclaw.model.apiKeyOptionalPlaceholder'
                  : 'picoclaw.model.apiKeyPlaceholder'
              )}
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
            />
          </SettingRow>
          <SettingRow label={t('picoclaw.model.apiBase')} htmlFor="picoclaw-base" stacked>
            <Input
              id="picoclaw-base"
              prefix={<LinkIcon size={13} />}
              placeholder={t('picoclaw.model.apiBasePlaceholder')}
              value={apiBase}
              onChange={(e) => setApiBase(e.target.value)}
            />
          </SettingRow>
          <div className="flex justify-end">
            <Button type="primary" loading={saving} onClick={() => void saveModel()}>
              {t(saving ? 'picoclaw.model.saving' : 'picoclaw.model.save')}
            </Button>
          </div>
        </Panel>
      </SettingsSection>

      <Panel className="space-y-3">
        <SettingRow
          label={t('picoclaw.model.hostTools')}
          description={t('picoclaw.model.hostToolsHint')}
          htmlFor="picoclaw-host-tools"
        >
          <Switch
            id="picoclaw-host-tools"
            aria-describedby="picoclaw-host-tools-description"
            checked={hostTools}
            loading={savingTools}
            disabled={!status || savingTools}
            onChange={(checked) => void changeTools(checked)}
          />
        </SettingRow>
        {hostTools && (
          <Alert type="warning" showIcon message={t('picoclaw.model.hostToolsWarning')} />
        )}
      </Panel>
    </div>
  );
};
