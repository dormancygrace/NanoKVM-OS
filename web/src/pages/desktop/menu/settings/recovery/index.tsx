import { useEffect, useState } from 'react';
import { Alert, Button, Checkbox, Input, Modal, Select, Spin } from 'antd';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';
import { Panel } from '@/components/ui/settings';

type Options = {
  action: 'restore' | 'reset';
  mode: 'migrate' | 'device';
  keepUsers: boolean;
  keepApps: boolean;
};
type Preview = {
  token: string;
  changed: string[];
  skipped: string[];
  removePackages: string[];
  packages: string[];
  version: string;
};
type Request = { password: string; archivePassword: string; archive: string; options: Options };
const maxArchive = 4 * 1024 * 1024;

async function call(action: string, data: unknown) {
  const response = await http.post(`/api/system/recovery/${action}`, data, { timeout: 180000 });
  if (response.code !== 0) throw new Error(response.msg);
  return response.data;
}
function encode(bytes: Uint8Array) {
  let binary = '';
  for (let i = 0; i < bytes.length; i += 8192)
    binary += String.fromCharCode(...bytes.subarray(i, i + 8192));
  return btoa(binary);
}

export const Recovery = () => {
  const { t } = useTranslation();
  const label = (key: string) => t(`settings.recovery.${key}`);
  const [password, setPassword] = useState('');
  const [archivePassword, setArchivePassword] = useState('');
  const [file, setFile] = useState<File>();
  const [options, setOptions] = useState<Options>({
    action: 'restore',
    mode: 'migrate',
    keepUsers: true,
    keepApps: true
  });
  const [preview, setPreview] = useState<{ data: Preview; request: Request }>();
  const [available, setAvailable] = useState(false);
  const [pending, setPending] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [status, setStatus] = useState('');
  const [checked, setChecked] = useState(false);

  useEffect(() => {
    let active = true;
    http
      .get('/api/system/recovery')
      .then((r) => {
        if (!active) return;
        if (r.code !== 0) throw new Error(r.msg);
        setAvailable(r.data.available);
        setPending(r.data.status.state === 'pending');
        setStatus(r.data.status.message || '');
      })
      .catch((e: Error) => {
        if (active) setError(e.message);
      });
    return () => {
      active = false;
    };
  }, []);

  async function perform(action: () => Promise<void>) {
    setBusy(true);
    setError('');
    try {
      await action();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  async function download() {
    await perform(async () => {
      const result = await call('export', { password, archivePassword });
      const bytes = Uint8Array.from(atob(result.archive), (c) => c.charCodeAt(0));
      const url = URL.createObjectURL(new Blob([bytes], { type: 'application/octet-stream' }));
      const anchor = document.createElement('a');
      anchor.href = url;
      anchor.download = 'NanoKVM-settings.nkos';
      anchor.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    });
  }
  async function inspect() {
    await perform(async () => {
      let archive = '';
      if (options.action === 'restore') {
        if (!file || file.size > maxArchive) throw new Error(label('fileLimit'));
        archive = encode(new Uint8Array(await file.arrayBuffer()));
      }
      const request = { password, archivePassword, archive, options: { ...options } };
      const data = await call('preview', request);
      setChecked(false);
      setPreview({ data, request });
    });
  }
  async function apply() {
    if (!preview || !checked) return;
    await perform(async () => {
      await call('apply', { ...preview.request, token: preview.data.token });
      setPreview(undefined);
      setPassword('');
      setArchivePassword('');
      setPending(true);
      setStatus(label('rebooting'));
    });
  }
  return (
    <div className="flex flex-col gap-4">
      <Alert type="info" showIcon title={label('scope')} />
      {error && <Alert type="error" showIcon title={error} />}
      {status && <Alert type={pending ? 'warning' : 'info'} showIcon title={status} />}
      <Panel>
        <label className="flex flex-col gap-2">
          {label('password')}
          <Input.Password
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={busy}
          />
        </label>
      </Panel>
      {pending ? (
        <Button
          disabled={busy || !password}
          onClick={() =>
            perform(async () => {
              await call('reboot', { password });
              setStatus(label('rebooting'));
            })
          }
        >
          {label('reboot')}
        </Button>
      ) : (
        <>
          <Panel>
            <h3 className="mb-3 font-semibold">{label('export')}</h3>
            <p className="text-fg-muted mb-3">{label('encryption')}</p>
            <label className="flex flex-col gap-2">
              {label('archivePassword')}
              <Input.Password
                autoComplete="new-password"
                value={archivePassword}
                onChange={(e) => setArchivePassword(e.target.value)}
                disabled={busy}
              />
            </label>
            <Button
              className="mt-3"
              disabled={busy || !password || archivePassword.length < 12}
              onClick={download}
            >
              {label('download')}
            </Button>
          </Panel>
          <Panel>
            <h3 className="mb-3 font-semibold">{label('restoreReset')}</h3>
            {!available && <Alert type="warning" title={label('updateBase')} />}
            <Select
              className="my-3 w-full"
              value={options.action}
              disabled={busy}
              onChange={(action) => setOptions({ ...options, action })}
              options={[
                { value: 'restore', label: label('restore') },
                { value: 'reset', label: label('reset') }
              ]}
            />
            {options.action === 'restore' ? (
              <div className="flex flex-col gap-3">
                <label>
                  {label('file')}
                  <input
                    className="mt-2 block w-full"
                    type="file"
                    accept=".nkos"
                    disabled={busy}
                    onChange={(e) => setFile(e.target.files?.[0])}
                  />
                </label>
                <Select
                  value={options.mode}
                  disabled={busy}
                  onChange={(mode) => setOptions({ ...options, mode })}
                  options={[
                    { value: 'migrate', label: label('migrate') },
                    { value: 'device', label: label('device') }
                  ]}
                />
                <p className="text-fg-muted">{label('migrationHint')}</p>
              </div>
            ) : (
              <div className="flex flex-col gap-3">
                <Checkbox
                  checked={options.keepUsers}
                  disabled={busy}
                  onChange={(e) => setOptions({ ...options, keepUsers: e.target.checked })}
                >
                  {label('keepUsers')}
                </Checkbox>
                <Checkbox
                  checked={options.keepApps}
                  disabled={busy}
                  onChange={(e) => setOptions({ ...options, keepApps: e.target.checked })}
                >
                  {label('keepApps')}
                </Checkbox>
                <p className="text-fg-muted">{label('resetHint')}</p>
                {!options.keepUsers && <Alert type="warning" title={label('resetUsers')} />}
              </div>
            )}
            <Button className="mt-4" disabled={busy || !available || !password} onClick={inspect}>
              {label('preview')}
            </Button>
          </Panel>
        </>
      )}
      {busy && <Spin />}
      <Modal
        open={!!preview}
        title={label('preview')}
        onCancel={() => {
          if (!busy) setPreview(undefined);
        }}
        closable={!busy}
        maskClosable={!busy}
        footer={[
          <Button key="cancel" disabled={busy} onClick={() => setPreview(undefined)}>
            {label('cancel')}
          </Button>,
          <Button
            key="apply"
            danger
            type="primary"
            loading={busy}
            disabled={!checked}
            onClick={apply}
          >
            {label('apply')}
          </Button>
        ]}
      >
        {error && <Alert type="error" showIcon title={error} />}
        <Alert type="warning" showIcon title={label('disconnect')} />
        <div className="my-3 max-h-72 overflow-auto break-all">
          {preview?.data.version && (
            <p>
              {label('source')}: {preview.data.version}
            </p>
          )}
          <strong>{label('changed')}</strong>
          <ul>
            {preview?.data.changed.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
          {!!preview?.data.skipped.length && (
            <>
              <strong>{label('skipped')}</strong>
              <ul>
                {preview.data.skipped.map((p) => (
                  <li key={p}>{p}</li>
                ))}
              </ul>
            </>
          )}
          {!!preview?.data.removePackages.length && (
            <>
              <strong>{label('removePackages')}</strong>
              <p>{preview.data.removePackages.join(', ')}</p>
            </>
          )}
          {!!preview?.data.packages.length && (
            <>
              <strong>{label('inventory')}</strong>
              <p>{preview.data.packages.join(', ')}</p>
            </>
          )}
        </div>
        <Checkbox checked={checked} disabled={busy} onChange={(e) => setChecked(e.target.checked)}>
          {label('confirm')}
        </Checkbox>
      </Modal>
    </div>
  );
};
