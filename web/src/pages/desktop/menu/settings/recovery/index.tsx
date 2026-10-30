import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Checkbox, Input, Modal, Radio, Spin } from 'antd';
import { FileUpIcon } from 'lucide-react';
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
const minBackupPassword = 12;

async function call<T>(action: string, data: unknown) {
  const response = await http.post<T>(`/api/system/recovery/${action}`, data, {
    timeout: 180000,
    sensitive: true
  });
  if (response.code !== 0) throw new Error(response.msg);
  return response.data;
}
function encode(bytes: Uint8Array) {
  let binary = '';
  for (let i = 0; i < bytes.length; i += 8192)
    binary += String.fromCharCode(...bytes.subarray(i, i + 8192));
  return btoa(binary);
}

// What the administrator password confirms: every action asks for it when
// it starts, not before one is chosen.
type Ask = {
  title: string;
  okText: string;
  danger?: boolean;
  run: (password: string) => Promise<void>;
};

// System > Backup & reset: export the settings, restore them from a file,
// or reset them. Restore and reset show what will change before applying.
export const Recovery = () => {
  const { t } = useTranslation();
  const label = (key: string) => t(`settings.recovery.${key}`);
  const [backupPassword, setBackupPassword] = useState('');
  const [archivePassword, setArchivePassword] = useState('');
  const [file, setFile] = useState<File>();
  const fileInput = useRef<HTMLInputElement>(null);
  const [mode, setMode] = useState<Options['mode']>('migrate');
  const [keepUsers, setKeepUsers] = useState(true);
  const [keepApps, setKeepApps] = useState(true);
  const [ask, setAsk] = useState<Ask>();
  const [adminPassword, setAdminPassword] = useState('');
  const [preview, setPreview] = useState<{ data: Preview; request: Request }>();
  const [checked, setChecked] = useState(false);
  const [available, setAvailable] = useState(false);
  const [pending, setPending] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [error, setError] = useState('');
  const [status, setStatus] = useState('');

  useEffect(() => {
    let active = true;
    http
      .get<{ available: boolean; status: { state: string; message?: string } }>(
        '/api/system/recovery'
      )
      .then((r) => {
        if (!active) return;
        if (r.code !== 0) throw new Error(r.msg);
        setAvailable(r.data.available);
        setPending(r.data.status.state === 'pending');
        setStatus(r.data.status.message || '');
        setLoaded(true);
      })
      .catch((e: Error) => {
        if (active) setLoadError(e.message);
      });
    return () => {
      active = false;
    };
  }, []);

  // The last dialog's texts stay while it fades out after closing.
  const shown = useRef<Ask>(undefined);
  function start(next: Ask) {
    setError('');
    setAdminPassword('');
    shown.current = next;
    setAsk(next);
  }
  // A typed but unused password does not stay in the page.
  function closeAsk() {
    setAsk(undefined);
    setAdminPassword('');
  }
  async function confirmAsk() {
    if (!ask || !adminPassword || busy) return;
    setBusy(true);
    setError('');
    try {
      await ask.run(adminPassword);
      closeAsk();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function download(password: string) {
    const result = await call<{ archive: string }>('export', {
      password,
      archivePassword: backupPassword
    });
    const bytes = Uint8Array.from(atob(result.archive), (c) => c.charCodeAt(0));
    const url = URL.createObjectURL(new Blob([bytes], { type: 'application/octet-stream' }));
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = 'NanoKVM-settings.nkos';
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  async function inspect(action: Options['action'], password: string) {
    let archive = '';
    if (action === 'restore') {
      if (!file || file.size > maxArchive) throw new Error(label('fileLimit'));
      archive = encode(new Uint8Array(await file.arrayBuffer()));
    }
    const request: Request = {
      password,
      archivePassword: action === 'restore' ? archivePassword : '',
      archive,
      options: { action, mode, keepUsers, keepApps }
    };
    const data = await call<Preview>('preview', request);
    setChecked(false);
    setPreview({ data, request });
  }
  async function apply() {
    if (!preview || !checked) return;
    setBusy(true);
    setError('');
    try {
      await call('apply', { ...preview.request, token: preview.data.token });
      setPreview(undefined);
      setBackupPassword('');
      setArchivePassword('');
      setPending(true);
      setStatus(label('rebooting'));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  if (!loaded) return loadError ? <Alert type="error" showIcon title={loadError} /> : <Spin />;
  const backupShort = backupPassword.length > 0 && backupPassword.length < minBackupPassword;

  return (
    <div className="flex flex-col gap-4">
      <p className="text-fg-muted m-0 text-sm">{label('scope')}</p>
      {status && <Alert type={pending ? 'warning' : 'info'} showIcon title={status} />}
      {pending ? (
        <div>
          <Button
            type="primary"
            onClick={() =>
              start({
                title: label('reboot'),
                okText: label('reboot'),
                run: async (password) => {
                  await call('reboot', { password });
                  setStatus(label('rebooting'));
                }
              })
            }
          >
            {label('reboot')}
          </Button>
        </div>
      ) : (
        <>
          <Panel>
            <h3 className="mt-0 mb-2 font-semibold">{label('export')}</h3>
            <p className="text-fg-muted mt-0 mb-3 text-sm">{label('encryption')}</p>
            <label className="flex flex-col gap-2">
              {label('backupPassword')}
              <Input.Password
                autoComplete="new-password"
                value={backupPassword}
                status={backupShort ? 'warning' : undefined}
                onChange={(e) => setBackupPassword(e.target.value)}
              />
            </label>
            {backupShort && (
              <div className="text-warning mt-1 text-xs">{label('backupPasswordShort')}</div>
            )}
            <Button
              className="mt-3"
              disabled={backupPassword.length < minBackupPassword}
              onClick={() =>
                start({ title: label('export'), okText: label('download'), run: download })
              }
            >
              {label('download')}
            </Button>
          </Panel>

          <Panel>
            <h3 className="mt-0 mb-3 font-semibold">{label('restore')}</h3>
            {!available && <Alert className="mb-3" type="warning" title={label('updateBase')} />}
            <div className="flex flex-col gap-3">
              <div>
                <input
                  ref={fileInput}
                  className="hidden"
                  type="file"
                  accept=".nkos"
                  onChange={(e) => setFile(e.target.files?.[0])}
                />
                <Button icon={<FileUpIcon size={16} />} onClick={() => fileInput.current?.click()}>
                  {label('chooseFile')}
                </Button>
                {file && <span className="text-fg-muted ml-3 text-sm break-all">{file.name}</span>}
              </div>
              <label className="flex flex-col gap-2">
                {label('archivePassword')}
                <Input.Password
                  autoComplete="off"
                  value={archivePassword}
                  onChange={(e) => setArchivePassword(e.target.value)}
                />
              </label>
              <Radio.Group
                className="flex! flex-col gap-2"
                value={mode}
                onChange={(e) => setMode(e.target.value)}
              >
                <Radio value="migrate">
                  <div>{label('migrate')}</div>
                  <div className="text-fg-muted text-xs">{label('migrationHint')}</div>
                </Radio>
                <Radio value="device">
                  <div>{label('device')}</div>
                  <div className="text-fg-muted text-xs">{label('deviceHint')}</div>
                </Radio>
              </Radio.Group>
            </div>
            <Button
              className="mt-4"
              disabled={!available || !file || !archivePassword}
              onClick={() =>
                start({
                  title: label('restore'),
                  okText: label('preview'),
                  run: (password) => inspect('restore', password)
                })
              }
            >
              {label('preview')}
            </Button>
          </Panel>

          <Panel>
            <h3 className="mt-0 mb-3 font-semibold">{label('reset')}</h3>
            <div className="flex flex-col gap-3">
              <Checkbox checked={keepUsers} onChange={(e) => setKeepUsers(e.target.checked)}>
                {label('keepUsers')}
              </Checkbox>
              <Checkbox checked={keepApps} onChange={(e) => setKeepApps(e.target.checked)}>
                {label('keepApps')}
              </Checkbox>
              <p className="text-fg-muted m-0 text-sm">{label('resetHint')}</p>
              {!keepUsers && <Alert type="warning" title={label('resetUsers')} />}
            </div>
            <Button
              className="mt-4"
              danger
              disabled={!available}
              onClick={() =>
                start({
                  title: label('reset'),
                  okText: label('preview'),
                  danger: true,
                  run: (password) => inspect('reset', password)
                })
              }
            >
              {label('preview')}
            </Button>
          </Panel>
        </>
      )}

      <Modal
        open={!!ask}
        title={shown.current?.title}
        onCancel={() => {
          if (!busy) closeAsk();
        }}
        closable={!busy}
        maskClosable={!busy}
        destroyOnHidden
        footer={[
          <Button key="cancel" disabled={busy} onClick={closeAsk}>
            {label('cancel')}
          </Button>,
          <Button
            key="ok"
            type="primary"
            danger={shown.current?.danger}
            loading={busy}
            disabled={!adminPassword}
            onClick={() => void confirmAsk()}
          >
            {shown.current?.okText}
          </Button>
        ]}
      >
        <div className="flex flex-col gap-3">
          {error && <Alert type="error" showIcon title={error} />}
          <label className="flex flex-col gap-2">
            {label('password')}
            <Input.Password
              autoFocus
              autoComplete="current-password"
              value={adminPassword}
              disabled={busy}
              onChange={(e) => setAdminPassword(e.target.value)}
              onPressEnter={() => void confirmAsk()}
            />
          </label>
        </div>
      </Modal>

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
            onClick={() => void apply()}
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
