import { ChangeEvent, useRef, useState } from 'react';
import { UploadOutlined } from '@ant-design/icons';
import { Button, Divider, Popconfirm, Switch } from 'antd';
import clsx from 'clsx';
import { ChevronRightIcon, FileJsonIcon, PowerIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/script.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { MenuItem } from '@/components/menu-item.tsx';

import { Run } from './run';

export const Script = () => {
  const { t } = useTranslation();

  const [scripts, setScripts] = useState<string[]>([]);
  // Scripts that run at startup, and their state in this boot.
  const [autostart, setAutostart] = useState<string[]>([]);
  const [autostartStatus, setAutostartStatus] = useState<Record<string, string>>({});
  const [isTogglingAutostart, setIsTogglingAutostart] = useState(false);
  const [currentScript, setCurrentScript] = useState('');
  const [isRunning, setIsRunning] = useState(false);

  const [isUploading, setIsUploading] = useState(false);
  const inputRef = useRef<any>(null);

  function handleOpenChange(open: boolean) {
    if (open) {
      getScripts();
    } else {
      setCurrentScript('');
    }
  }

  function selectFile() {
    if (inputRef.current) {
      inputRef.current.value = null;
    }

    inputRef.current?.click();
  }

  function uploadFile(e: ChangeEvent<HTMLInputElement>) {
    if (!e.target?.files?.length) return;
    const file = e.target.files[0];

    if (isUploading) return;
    setIsUploading(true);

    const formData = new FormData();
    formData.append('file', file);

    api
      .uploadScript(formData)
      .then((rsp) => {
        if (rsp.code !== 0) {
          showRequestError(rsp);
          return;
        }

        if (!scripts.includes(rsp.data.file)) {
          setScripts([...scripts, rsp.data.file]);
        }
      })
      .catch((err) => showRequestError(err))
      .finally(() => {
        setIsUploading(false);
      });
  }

  function runScript(type: string) {
    if (!currentScript) return;

    if (type === 'foreground') {
      setIsRunning(true);
    } else {
      api
        .runScript(currentScript, type)
        .then((rsp) => {
          if (rsp.code !== 0) showRequestError(rsp, 'script.runFailed');
        })
        .catch((err) => showRequestError(err, 'script.runFailed'));
    }
  }

  function getScripts() {
    api
      .getScripts()
      .then((rsp) => {
        if (rsp.code !== 0) {
          showRequestError(rsp);
          return;
        }

        if (rsp.data?.files?.length > 0) {
          setScripts(rsp.data.files);
        }
        setAutostart(rsp.data?.autostart ?? []);
        setAutostartStatus(rsp.data?.autostartStatus ?? {});
      })
      .catch((err) => showRequestError(err));
  }

  function deleteScript() {
    if (!currentScript) return;

    api
      .deleteScript(currentScript)
      .then((rsp) => {
        if (rsp.code !== 0) {
          showRequestError(rsp);
          return;
        }

        setScripts(scripts.filter((script) => script !== currentScript));
        setAutostart(autostart.filter((script) => script !== currentScript));
      })
      .catch((err) => showRequestError(err));
  }

  function toggleAutostart(enabled: boolean) {
    if (!currentScript || isTogglingAutostart) return;
    const script = currentScript;
    setIsTogglingAutostart(true);
    api
      .setScriptAutostart(script, enabled)
      .then((rsp) => {
        if (rsp.code !== 0) {
          showRequestError(rsp);
          return;
        }
        setAutostart(
          enabled ? [...autostart, script] : autostart.filter((name) => name !== script)
        );
      })
      .catch((err) => showRequestError(err))
      .finally(() => setIsTogglingAutostart(false));
  }

  // This boot's state of a script that runs at startup.
  function startupState(script: string) {
    const status = autostartStatus[script];
    if (!status) return '';
    if (status === 'running') return t('script.autostartRunning');
    const exited = /^exited (\d+)$/.exec(status);
    if (exited) return t('script.autostartExited', { code: exited[1] });
    return t('script.autostartFailed');
  }

  function activate(script: string) {
    setCurrentScript(script === currentScript ? '' : script);
  }

  const content = (
    <div className="min-w-[250px]">
      <div className="flex items-center justify-between">
        <span className="text-base font-bold text-neutral-300">{t('script.title')}</span>

        <input
          ref={inputRef}
          type="file"
          accept=".sh,.py"
          className="hidden"
          onChange={uploadFile}
        />
        <Button
          ghost
          type="primary"
          size="small"
          icon={<UploadOutlined />}
          loading={isUploading}
          onClick={selectFile}
        >
          {t('script.upload')}
        </Button>
      </div>
      <Divider style={{ margin: '10px 0 15px 0' }} />

      {scripts.map((script) => (
        <div
          key={script}
          className={clsx(
            'my-1 cursor-pointer rounded',
            script === currentScript ? 'bg-neutral-700/50' : 'hover:bg-neutral-700/70'
          )}
        >
          <div
            className="flex items-center justify-between space-x-5 px-2 py-1.5"
            onClick={() => activate(script)}
          >
            <div className="flex max-w-[300px] items-center gap-1.5 select-none">
              <span className="truncate">{script}</span>
              {autostart.includes(script) && (
                <PowerIcon
                  size={12}
                  className="text-fg-muted shrink-0"
                  aria-label={t('script.autostart')}
                />
              )}
            </div>
            <div className={clsx('h-[16px] w-[16px]', script === currentScript && 'rotate-90')}>
              <ChevronRightIcon size={16} />
            </div>
          </div>

          {script === currentScript && (
            <div className="space-y-1 px-3 pt-2">
              <label className="flex items-center justify-between gap-3 text-sm">
                <span>{t('script.autostart')}</span>
                <Switch
                  size="small"
                  checked={autostart.includes(script)}
                  loading={isTogglingAutostart}
                  onChange={toggleAutostart}
                />
              </label>
              <p className="text-fg-muted m-0 text-xs">
                {startupState(script) || t('script.autostartHint')}
              </p>
            </div>
          )}

          {script === currentScript && (
            <div className="flex items-center justify-end space-x-2 p-3">
              <Button type="primary" size="small" onClick={() => runScript('foreground')}>
                {t('script.run')}
              </Button>
              <Button type="primary" size="small" onClick={() => runScript('background')}>
                {t('script.runBackground')}
              </Button>

              <Popconfirm
                title={t('script.attention')}
                description={t('script.delDesc')}
                onConfirm={deleteScript}
                onCancel={() => {}}
                okText={t('script.confirm')}
                cancelText={t('script.cancel')}
                placement="bottom"
              >
                <Button danger type="primary" size="small">
                  {t('script.delete')}
                </Button>
              </Popconfirm>
            </div>
          )}
        </div>
      ))}
    </div>
  );

  return (
    <>
      <MenuItem
        title={t('script.title')}
        icon={<FileJsonIcon size={18} />}
        content={content}
        onOpenChange={handleOpenChange}
      />

      {isRunning && <Run script={currentScript} setIsRunning={setIsRunning} />}
    </>
  );
};
