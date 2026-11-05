import { useEffect, useRef, useState, type DragEvent } from 'react';
import { Alert, Button, Input, Progress } from 'antd';
import { DownloadIcon, FileIcon, FolderIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as storage from '@/api/storage.ts';
import { confirmAction } from '@/components/ui/confirm.ts';

import { dropEntries, planFromDrop, planFromFiles, type UploadPlan } from './user-disk-upload.ts';

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
  const [count, setCount] = useState({ done: 0, total: 0 });
  const [summary, setSummary] = useState('');
  const [dragging, setDragging] = useState(false);
  const running = useRef(false);
  const abort = useRef<AbortController | null>(null);
  const picker = useRef<HTMLInputElement>(null);
  const folderPicker = useRef<HTMLInputElement>(null);
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

  // A name that exists or is invalid on FAT32 is skipped, so the same folder
  // can be uploaded again after a partial failure. Disk-level errors stop.
  async function upload(plan: UploadPlan) {
    if (!plan.files.length && !plan.dirs.length) return;
    setSummary('');
    await run(async () => {
      const signal = (abort.current = new AbortController()).signal;
      const skipped: string[] = [];
      const under = (path: string) => skipped.some((dir) => path.startsWith(dir + '/'));
      const skip = (msg: string) => msg === 'exists' || msg === 'path';
      let done = 0;
      setCount({ done, total: plan.files.length });
      try {
        for (const dir of plan.dirs) {
          if (signal.aborted) break;
          if (under(dir)) continue;
          setUploadName(dir + '/');
          setProgress(0);
          const rsp = await storage.makeUserDiskDirectory(join(dir), true);
          if (rsp.code === 0) continue;
          if (!skip(rsp.msg)) return failure(rsp.msg);
          skipped.push(dir);
        }
        for (const { path, file } of plan.files) {
          if (under(path)) continue;
          setUploadName(path);
          setProgress(0);
          const rsp = await storage.uploadUserDiskFile(join(path), file, signal, setProgress);
          if (rsp.code === 0) setCount({ done: ++done, total: plan.files.length });
          else if (skip(rsp.msg)) skipped.push(path);
          else return failure(rsp.msg);
        }
      } finally {
        if (plan.files.length > 1 || skipped.length) {
          const names = skipped.slice(0, 10).join(', ') + (skipped.length > 10 ? ', …' : '');
          setSummary(
            t(key + 'uploaded', { done, total: plan.files.length }) +
              (skipped.length ? ' ' + t(key + 'skipped', { names }) : '')
          );
        }
        if (!signal.aborted) await refresh(folder);
      }
    });
  }

  // Keep the browser from opening a dropped file in place of this page, even
  // while uploads are not possible.
  const dropProps = {
    onDragOver: (event: DragEvent) => {
      if (!event.dataTransfer.types.includes('Files')) return;
      event.preventDefault();
      const ok = !blocked && !!listing;
      event.dataTransfer.dropEffect = ok ? 'copy' : 'none';
      setDragging(ok);
    },
    onDragLeave: (event: DragEvent) => {
      if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragging(false);
    },
    onDrop: (event: DragEvent) => {
      if (!event.dataTransfer.types.includes('Files')) return;
      event.preventDefault();
      setDragging(false);
      if (blocked || !listing || running.current) return;
      const dropped = dropEntries(event.dataTransfer);
      void planFromDrop(dropped)
        .then(upload)
        .catch(() => failure('io'));
    }
  };

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
    <section
      className={
        'text-fg border-line min-w-0 space-y-3 rounded-sm border-t pt-4' +
        (dragging ? ' outline-2 outline-offset-4 outline-blue-500 outline-dashed' : '')
      }
      {...dropProps}
    >
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
                if (files.length) void upload(planFromFiles(files));
              }}
            />
            <Button disabled={blocked || !listing} onClick={() => folderPicker.current?.click()}>
              {t(key + 'uploadFolder')}
            </Button>
            <input
              ref={folderPicker}
              type="file"
              multiple
              {...{ webkitdirectory: '' }}
              className="hidden"
              aria-label={t(key + 'uploadFolder')}
              onChange={(event) => {
                const files = Array.from(event.target.files ?? []);
                event.target.value = '';
                if (files.length) void upload(planFromFiles(files));
              }}
            />
          </div>
          {listing && <p className="text-fg-muted m-0 text-xs">{t(key + 'dropHint')}</p>}
          {progress !== undefined && (
            <div className="space-y-2">
              {count.total > 1 && (
                <div className="text-fg-muted text-xs">
                  {t(key + 'progress', { done: count.done, total: count.total })}
                </div>
              )}
              <div className="text-sm break-all">{uploadName}</div>
              <Progress percent={progress} status="active" />
              <div className="text-fg-muted text-xs">{t(key + 'transferring')}</div>
              <Button onClick={() => abort.current?.abort()}>{t(key + 'cancel')}</Button>
            </div>
          )}
          {listing && (
            <>
              <nav className="flex flex-wrap items-center gap-1 text-sm break-all">
                {['', ...(folder === '.' ? [] : folder.split('/'))].map((part, i, parts) => {
                  const target = parts.slice(1, i + 1).join('/') || '.';
                  return (
                    <span key={i} className="flex items-center gap-1">
                      {i > 1 && <span className="text-fg-muted">/</span>}
                      {i === parts.length - 1 ? (
                        <span>{part || '/'}</span>
                      ) : (
                        <button
                          className="nanokvm-button-base cursor-pointer underline underline-offset-4 disabled:cursor-default disabled:opacity-50"
                          disabled={blocked}
                          onClick={() => void run(() => refresh(target))}
                        >
                          {part || '/'}
                        </button>
                      )}
                    </span>
                  );
                })}
              </nav>
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
      {summary && (
        <Alert type="info" showIcon closable title={summary} onClose={() => setSummary('')} />
      )}
      {error && <Alert type="error" showIcon title={error} />}
    </section>
  );
};
