import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Input, Progress } from 'antd';
import { DownloadIcon, FileIcon, FolderIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as storage from '@/api/storage.ts';
import { confirmAction } from '@/components/ui/confirm.ts';

const bytes = (value: number) =>
  value >= 1048576 ? (value / 1048576).toFixed(1) + ' MiB' : (value / 1024).toFixed(1) + ' KiB';

export const UserDiskFiles = ({
  host,
  busy,
  onBusy
}: {
  host: boolean;
  busy: boolean;
  onBusy: (value: boolean) => void;
}) => {
  const { t } = useTranslation();
  const [listing, setListing] = useState<storage.UserDiskListing>();
  const [folder, setFolder] = useState('.');
  const [newFolder, setNewFolder] = useState('');
  const [error, setError] = useState('');
  const [progress, setProgress] = useState<number>();
  const [uploadName, setUploadName] = useState('');
  const running = useRef(false);
  const abort = useRef<AbortController | null>(null);
  const picker = useRef<HTMLInputElement>(null);
  const key = 'settings.usb.userDisk.files.';
  const blocked = host || busy;
  const join = (name: string) => (folder === '.' ? name : folder + '/' + name);

  useEffect(() => () => abort.current?.abort(), []);
  useEffect(() => {
    // A previous list is stale once the computer can write to the disk.
    if (host) setListing(undefined);
  }, [host]);

  function failure(reason: string) {
    const known = ['host', 'busy', 'path', 'exists', 'missing', 'space', 'notEmpty'];
    setError(t(key + 'errors.' + (known.includes(reason) ? reason : 'io')));
  }

  async function refresh(path: string, offset = 0) {
    const rsp = await storage.listUserDiskFiles(path, offset);
    if (rsp.code !== 0) {
      setListing(undefined);
      failure(rsp.msg);
      return;
    }
    setFolder(path);
    setListing(rsp.data);
  }

  async function run(action: () => Promise<void>) {
    if (running.current || host || busy) return;
    running.current = true;
    onBusy(true);
    setError('');
    try {
      await action();
    } catch {
      if (!abort.current?.signal.aborted) failure('io');
    } finally {
      running.current = false;
      abort.current = null;
      onBusy(false);
      setProgress(undefined);
    }
  }

  async function upload(files: File[]) {
    await run(async () => {
      abort.current = new AbortController();
      for (const file of files) {
        setUploadName(file.name);
        setProgress(0);
        const rsp = await storage.uploadUserDiskFile(
          join(file.name),
          file,
          abort.current.signal,
          setProgress
        );
        if (rsp.code !== 0) {
          failure(rsp.msg);
          break;
        }
      }
      await refresh(folder);
    });
  }

  async function remove(entry: storage.UserDiskEntry) {
    const ok = await confirmAction({
      title: t(key + 'deleteTitle'),
      content: t(key + 'deleteConfirm', { name: entry.name }),
      danger: true
    });
    if (!ok) return;
    await run(async () => {
      const rsp = await storage.deleteUserDiskFile(join(entry.name));
      if (rsp.code !== 0) failure(rsp.msg);
      await refresh(folder);
    });
  }

  return (
    <section className="text-fg border-line min-w-0 space-y-3 border-t pt-4">
      <div className="font-medium">{t(key + 'title')}</div>
      {host ? (
        <Alert type="info" showIcon title={t(key + 'host')} />
      ) : (
        <>
          <p className="text-fg-muted m-0 text-xs">{t(key + 'hint')}</p>
          <div className="flex flex-wrap gap-2">
            <Button
              disabled={blocked}
              loading={busy && progress === undefined}
              onClick={() => void run(() => refresh(folder))}
            >
              {t(key + (listing ? 'refresh' : 'browse'))}
            </Button>
            {folder !== '.' && (
              <Button
                disabled={blocked}
                onClick={() =>
                  void run(() => refresh(folder.split('/').slice(0, -1).join('/') || '.'))
                }
              >
                {t(key + 'up')}
              </Button>
            )}
            <Button disabled={blocked || !listing} onClick={() => picker.current?.click()}>
              {t(key + 'upload')}
            </Button>
            <input
              ref={picker}
              type="file"
              multiple
              className="hidden"
              aria-label={t(key + 'upload')}
              onChange={(event) => {
                const files = Array.from(event.target.files ?? []);
                event.target.value = '';
                if (files.length) void upload(files);
              }}
            />
          </div>
          {progress !== undefined && (
            <div className="space-y-2">
              <div className="text-sm break-all">{uploadName}</div>
              <Progress percent={progress} status="active" />
              <div className="text-fg-muted text-xs">{t(key + 'transferring')}</div>
              <Button onClick={() => abort.current?.abort()}>{t(key + 'cancel')}</Button>
            </div>
          )}
          {listing && (
            <>
              <div className="text-sm break-all">/{folder === '.' ? '' : folder}</div>
              <div className="text-fg-muted text-xs">
                {t(key + 'space', { free: bytes(listing.free), total: bytes(listing.total) })}
              </div>
              <div className="flex flex-wrap gap-2">
                <Input
                  className="min-w-0 flex-1"
                  value={newFolder}
                  disabled={blocked}
                  placeholder={t(key + 'folderName')}
                  aria-label={t(key + 'folderName')}
                  onChange={(event) => setNewFolder(event.target.value)}
                />
                <Button
                  disabled={blocked || !newFolder || /[/\\]/.test(newFolder)}
                  onClick={() =>
                    void run(async () => {
                      const rsp = await storage.makeUserDiskDirectory(join(newFolder));
                      if (rsp.code !== 0) failure(rsp.msg);
                      else setNewFolder('');
                      await refresh(folder);
                    })
                  }
                >
                  {t(key + 'mkdir')}
                </Button>
              </div>
              <ul className="m-0 max-h-80 list-none overflow-y-auto p-0">
                {listing.entries.map((entry) => (
                  <li
                    key={entry.name}
                    className="border-line flex min-w-0 flex-wrap items-center gap-2 border-b py-2"
                  >
                    {entry.directory ? <FolderIcon size={18} /> : <FileIcon size={18} />}
                    <div className="min-w-0 flex-1 basis-1/2">
                      {entry.directory ? (
                        <button
                          className="nanokvm-button-base max-w-full cursor-pointer text-left break-all underline underline-offset-4 disabled:cursor-default disabled:opacity-50"
                          disabled={blocked}
                          onClick={() => void run(() => refresh(join(entry.name)))}
                        >
                          {entry.name}
                        </button>
                      ) : (
                        <span className="break-all">{entry.name}</span>
                      )}
                      <div className="text-fg-muted text-xs">
                        {!entry.directory && bytes(entry.size) + ' · '}
                        {new Date(entry.modifiedAt).toLocaleString()}
                      </div>
                    </div>
                    {!entry.directory && (
                      <Button
                        type="text"
                        disabled={blocked}
                        icon={<DownloadIcon size={16} />}
                        aria-label={t(key + 'download') + ': ' + entry.name}
                        href={blocked ? undefined : storage.userDiskDownloadUrl(join(entry.name))}
                        target="_blank"
                        rel="noopener noreferrer"
                      />
                    )}
                    <Button
                      type="text"
                      danger
                      disabled={blocked}
                      icon={<Trash2Icon size={16} />}
                      aria-label={t(key + 'delete') + ': ' + entry.name}
                      onClick={() => void remove(entry)}
                    />
                  </li>
                ))}
                {!listing.entries.length && (
                  <li className="text-fg-muted py-3">{t(key + 'empty')}</li>
                )}
              </ul>
              <div className="flex gap-2">
                {listing.offset > 0 && (
                  <Button
                    disabled={blocked}
                    onClick={() =>
                      void run(() => refresh(folder, Math.max(0, listing.offset - 200)))
                    }
                  >
                    {t(key + 'previous')}
                  </Button>
                )}
                {listing.next && (
                  <Button
                    disabled={blocked}
                    onClick={() => void run(() => refresh(folder, listing.offset + 200))}
                  >
                    {t(key + 'next')}
                  </Button>
                )}
              </div>
            </>
          )}
        </>
      )}
      {error && <Alert type="error" showIcon title={error} />}
    </section>
  );
};
