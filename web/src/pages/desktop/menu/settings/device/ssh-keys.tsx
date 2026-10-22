import { useRef, useState } from 'react';
import { Alert, Button, Input, message, Modal } from 'antd';
import { useTranslation } from 'react-i18next';

import { importSSHKeys } from '@/api/vm.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

const maxBytes = 32 * 1024;
const validInput = (text: string) =>
  new TextEncoder().encode(text).length <= maxBytes &&
  !/PRIVATE KEY|PuTTY-User-Key-File/i.test(text);

type ImportResult = { added: number; skipped: number; fingerprints: string[] };

export const SshKeys = () => {
  const { t } = useTranslation();
  const fileInput = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);
  const [keys, setKeys] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<ImportResult | null>(null);

  function close() {
    if (busy) return;
    setOpen(false);
    setPassword('');
    setKeys('');
    setResult(null);
  }

  async function readFile(file: File) {
    setBusy(true);
    setResult(null);
    setKeys('');
    try {
      if (file.size > maxBytes) throw new Error('size');
      const text = await file.text();
      if (!validInput(text)) throw new Error('private key or size');
      setKeys(text);
    } catch {
      void message.error(t('sshKeys.invalid'));
    } finally {
      setBusy(false);
    }
  }

  async function submit() {
    if (busy) return;
    if (!password) {
      void message.error(t('auth.noEmptyPassword'));
      return;
    }
    if (!validInput(keys)) {
      void message.error(t('sshKeys.invalid'));
      return;
    }
    setBusy(true);
    setResult(null);
    try {
      const rsp = await importSSHKeys(keys, password);
      if (rsp.code !== 0) {
        void message.error(
          t(
            rsp.code === -3
              ? 'auth.invalidCurrentPassword'
              : rsp.code === -1
                ? 'sshKeys.invalid'
                : 'sshKeys.failed'
          )
        );
        return;
      }
      setResult(rsp.data as ImportResult);
      setPassword('');
      setKeys('');
    } catch (error) {
      showRequestError(error, 'sshKeys.failed');
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <SettingRow label={t('sshKeys.title')} description={t('sshKeys.description')}>
        <Button onClick={() => setOpen(true)}>{t('sshKeys.import')}</Button>
      </SettingRow>
      <Modal
        title={t('sshKeys.title')}
        open={open}
        onCancel={close}
        onOk={() => void submit()}
        okText={t('sshKeys.import')}
        cancelText={t('auth.cancel')}
        okButtonProps={{ disabled: busy || !keys.trim() || !password }}
        cancelButtonProps={{ disabled: busy }}
        confirmLoading={busy}
        closable={!busy}
        maskClosable={!busy}
        destroyOnHidden
      >
        <p className="text-fg-muted mb-4 text-sm">{t('sshKeys.help')}</p>
        <Button className="mb-4" disabled={busy} onClick={() => fileInput.current?.click()}>
          {t('sshKeys.file')}
        </Button>
        <input
          ref={fileInput}
          id="ssh-public-key-file"
          type="file"
          hidden
          accept=".pub,.txt,text/plain"
          disabled={busy}
          onChange={(event) => {
            const file = event.target.files?.[0];
            event.target.value = '';
            if (file) void readFile(file);
          }}
        />
        <label className="mb-2 block" htmlFor="ssh-public-key-text">
          {t('sshKeys.text')}
        </label>
        <Input.TextArea
          id="ssh-public-key-text"
          value={keys}
          rows={5}
          disabled={busy}
          spellCheck={false}
          autoComplete="off"
          placeholder="ssh-ed25519 AAAA… user@computer"
          onChange={(event) => {
            setKeys(event.target.value);
            setResult(null);
          }}
        />
        <label className="mt-4 mb-2 block" htmlFor="ssh-key-password">
          {t('auth.placeholderCurrentPassword')}
        </label>
        <Input.Password
          id="ssh-key-password"
          value={password}
          autoComplete="current-password"
          disabled={busy}
          onChange={(event) => setPassword(event.target.value)}
        />
        {result && (
          <Alert
            className="mt-4"
            type="success"
            showIcon
            title={t('sshKeys.result', { added: result.added, skipped: result.skipped })}
            description={result.fingerprints.map((fingerprint) => (
              <div key={fingerprint} className="font-mono text-xs break-all">
                {fingerprint}
              </div>
            ))}
          />
        )}
      </Modal>
    </>
  );
};
