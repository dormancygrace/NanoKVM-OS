import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Alert, message, Modal, Switch } from 'antd';
import { useSetAtom } from 'jotai';
import { CheckIcon, CopyIcon, EyeIcon, EyeOffIcon, RefreshCcwIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/mcp.ts';
import type { MCPConfig } from '@/api/mcp.ts';
import { getBaseUrl } from '@/lib/service.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { aiControlStatusAtom, normalizeAIControlStatus } from '@/jotai/ai-control.ts';
import { IconButton, Panel, SettingRow } from '@/components/ui/settings.tsx';

function maskKey(key: string) {
  if (!key) return '-';
  if (key.length <= 16) return `${key.slice(0, 4)}...${key.slice(-4)}`;
  return `${key.slice(0, 12)}...${key.slice(-6)}`;
}

function responseMessage(rsp: { msg?: string; message?: string }) {
  return rsp.msg || rsp.message || '';
}

async function writeClipboardText(text: string) {
  if (window.isSecureContext === true && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // Fall through to the legacy path for HTTP deployments and browser quirks.
    }
  }

  const textArea = document.createElement('textarea');
  const selection = document.getSelection();
  const selectedRange = selection && selection.rangeCount > 0 ? selection.getRangeAt(0) : null;

  textArea.value = text;
  textArea.setAttribute('readonly', '');
  textArea.style.position = 'fixed';
  textArea.style.left = '-9999px';
  textArea.style.top = '0';
  document.body.appendChild(textArea);
  textArea.focus();
  textArea.select();
  textArea.setSelectionRange(0, text.length);

  try {
    if (!document.execCommand('copy')) {
      throw new Error('copy command failed');
    }
  } finally {
    document.body.removeChild(textArea);
    if (selectedRange && selection) {
      selection.removeAllRanges();
      selection.addRange(selectedRange);
    }
  }
}

export const MCP = () => {
  const { t } = useTranslation();
  const [modal, contextHolder] = Modal.useModal();
  const setAIControlStatus = useSetAtom(aiControlStatusAtom);
  const [config, setConfig] = useState<MCPConfig>({
    enabled: false,
    apiKey: '',
    controlMode: 'picoclaw',
    transitioning: false
  });
  const [isLoading, setIsLoading] = useState(false);
  const [loadFailed, setLoadFailed] = useState(false);
  const [isKeyVisible, setIsKeyVisible] = useState(false);
  const [isEndpointCopied, setIsEndpointCopied] = useState(false);
  const [isKeyCopied, setIsKeyCopied] = useState(false);
  const isLoadingRef = useRef(false);
  const silentRefreshRef = useRef(false);
  const actionVersionRef = useRef(0);

  const endpoint = useMemo(() => `${getBaseUrl('http')}/api/mcp`, []);
  const displayKey = isKeyVisible ? config.apiKey || '-' : maskKey(config.apiKey);

  const syncConfig = useCallback(
    (nextConfig: MCPConfig) => {
      setConfig(nextConfig);
      const nextControlStatus = normalizeAIControlStatus(nextConfig, 'mcp_config');
      if (nextControlStatus) {
        setAIControlStatus(nextControlStatus);
      }
    },
    [setAIControlStatus]
  );

  const updateLoading = useCallback((loading: boolean) => {
    isLoadingRef.current = loading;
    setIsLoading(loading);
  }, []);

  const getConfig = useCallback(
    (silent = false) => {
      if (silent) {
        if (isLoadingRef.current || silentRefreshRef.current) return;
        silentRefreshRef.current = true;
      } else {
        updateLoading(true);
      }
      const actionVersion = actionVersionRef.current;

      api
        .getMCPConfig()
        .then((rsp) => {
          if (rsp.code !== 0) {
            if (!silent) setLoadFailed(true);
            return;
          }
          if (silent && actionVersion !== actionVersionRef.current) return;
          setLoadFailed(false);
          syncConfig(rsp.data);
          if (!rsp.data.enabled) setIsKeyVisible(false);
        })
        .catch(() => {
          if (!silent) setLoadFailed(true);
        })
        .finally(() => {
          if (silent) silentRefreshRef.current = false;
          else updateLoading(false);
        });
    },
    [syncConfig, updateLoading]
  );

  useEffect(() => {
    getConfig();
    const stopPolling = pollWhileVisible(() => getConfig(true), 3000);
    return () => stopPolling();
  }, [getConfig]);

  function updateEnabled(enabled: boolean) {
    if (isLoading) return;
    actionVersionRef.current += 1;
    updateLoading(true);
    api
      .setMCPEnabled(enabled)
      .then((rsp) => {
        if (rsp.code !== 0) {
          message.error(responseMessage(rsp) || t('settings.mcp.failed'));
          return;
        }
        syncConfig(rsp.data);
        if (!enabled) setIsKeyVisible(false);
      })
      .catch((err) => showRequestError(err, 'settings.mcp.failed'))
      .finally(() => updateLoading(false));
  }

  function setEnabled(enabled: boolean) {
    if (!enabled) {
      updateEnabled(false);
      return;
    }

    modal.confirm({
      title: t('settings.mcp.enableConfirmTitle'),
      content: <span className="text-fg-muted text-sm">{t('settings.mcp.enableConfirmDesc')}</span>,
      okText: t('settings.mcp.okBtn'),
      cancelText: t('settings.mcp.cancelBtn'),
      onOk: () => updateEnabled(true)
    });
  }

  async function copyText(text: string, type: 'endpoint' | 'key') {
    if (!text) return;
    try {
      await writeClipboardText(text);
      const setCopied = type === 'endpoint' ? setIsEndpointCopied : setIsKeyCopied;
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      message.error(
        t('settings.mcp.copyFailed', {
          defaultValue: 'Copy failed. Copy manually.'
        })
      );
    }
  }

  function regenerateKey() {
    if (isLoading || !config.enabled) return;
    modal.confirm({
      title: t('settings.mcp.regenerateConfirmTitle'),
      content: (
        <span className="text-fg-muted text-sm">{t('settings.mcp.regenerateConfirmDesc')}</span>
      ),
      okText: t('settings.mcp.okBtn'),
      cancelText: t('settings.mcp.cancelBtn'),
      onOk: async () => {
        actionVersionRef.current += 1;
        updateLoading(true);
        try {
          const rsp = await api.regenerateMCPAPIKey();
          if (rsp.code !== 0) {
            message.error(responseMessage(rsp) || t('settings.mcp.failed'));
            return;
          }
          syncConfig(rsp.data);
          setIsKeyVisible(false);
        } catch (err) {
          showRequestError(err, 'settings.mcp.failed');
        } finally {
          updateLoading(false);
        }
      }
    });
  }

  return (
    <>
      {contextHolder}

      <div className="space-y-6">
        {loadFailed && <Alert type="error" showIcon message={t('settings.mcp.failed')} />}
        <Alert type="warning" showIcon message={t('settings.mcp.securityWarning')} />

        <SettingRow
          label={t('settings.mcp.service')}
          description={t('settings.mcp.serviceDesc')}
          htmlFor="mcp-service"
        >
          <Switch
            id="mcp-service"
            aria-label={t('settings.mcp.service')}
            aria-describedby="mcp-service-description"
            checked={config.enabled}
            loading={isLoading || config.transitioning}
            disabled={config.transitioning}
            onChange={(enabled) => setEnabled(enabled)}
          />
        </SettingRow>

        {config.enabled && (
          <div className="animate-in fade-in slide-in-from-top-2 duration-300">
            <Panel flush className="divide-line divide-y overflow-hidden">
              <CredentialRow
                label={t('settings.mcp.endpoint')}
                value={endpoint}
                copyLabel={t('settings.mcp.copyValue', { label: t('settings.mcp.endpoint') })}
                copied={isEndpointCopied}
                onCopy={() => copyText(endpoint, 'endpoint')}
              />
              <CredentialRow
                label={t('settings.mcp.apiKey')}
                value={displayKey}
                copyLabel={t('settings.mcp.copyValue', { label: t('settings.mcp.apiKey') })}
                copied={isKeyCopied}
                onCopy={() => copyText(config.apiKey, 'key')}
                disabled={!config.apiKey}
                actions={
                  <>
                    <IconButton
                      label={t(isKeyVisible ? 'settings.mcp.hideKey' : 'settings.mcp.showKey')}
                      className="text-fg-muted hover:text-fg"
                      icon={isKeyVisible ? <EyeOffIcon size={14} /> : <EyeIcon size={14} />}
                      disabled={!config.apiKey}
                      onClick={() => setIsKeyVisible((visible) => !visible)}
                    />
                    <IconButton
                      label={t('settings.mcp.regenerateKey')}
                      className="text-fg-muted hover:text-fg"
                      loading={isLoading}
                      icon={<RefreshCcwIcon size={14} />}
                      onClick={regenerateKey}
                    />
                  </>
                }
              />
            </Panel>
          </div>
        )}
      </div>
    </>
  );
};

type CredentialRowProps = {
  label: string;
  value: string;
  copyLabel: string;
  copied: boolean;
  onCopy: () => void;
  disabled?: boolean;
  actions?: ReactNode;
};

const CredentialRow = ({
  label,
  value,
  copyLabel,
  copied,
  onCopy,
  disabled = false,
  actions
}: CredentialRowProps) => (
  <div className="group hover:bg-surface-raised flex flex-col gap-2 px-4 py-3 transition-colors sm:flex-row sm:items-center sm:justify-between">
    <span className="text-fg-muted w-24 shrink-0 text-sm font-medium">{label}</span>
    <div className="flex min-w-0 items-center justify-between gap-2">
      <span className="text-fg min-w-0 flex-1 truncate font-mono text-sm select-all">{value}</span>
      <div className="flex shrink-0 items-center space-x-1 opacity-40 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100 sm:ml-4">
        {actions}
        <IconButton
          label={copyLabel}
          className="text-fg-muted hover:text-fg"
          icon={copied ? <CheckIcon size={14} className="text-success" /> : <CopyIcon size={14} />}
          disabled={disabled}
          onClick={onCopy}
        />
      </div>
    </div>
  </div>
);
