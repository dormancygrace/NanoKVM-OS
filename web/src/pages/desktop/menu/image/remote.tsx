import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Input } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/storage.ts';
import { getBaseUrl } from '@/lib/service.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';

type Props = { isOpen: boolean; source: 'browser' | 'http'; onConnected: (value: boolean) => void };
type Status = api.RemoteMediaStatus;

export const RemoteImage = ({ isOpen, source, onConnected }: Props) => {
  const { t } = useTranslation();
  const [file, setFile] = useState<File | null>(null);
  const [status, setStatus] = useState<Status>({ connected: false });
  const [connecting, setConnecting] = useState(false);
  const [error, setError] = useState('');
  const [url, setUrl] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const socket = useRef<WebSocket | null>(null);
  const callback = useRef(onConnected);
  callback.current = onConnected;

  useEffect(
    () => () => {
      socket.current?.close();
    },
    []
  );
  useEffect(() => {
    if (!isOpen && !status.connected && !connecting) return;
    let disposed = false;
    const refresh = () =>
      api
        .getRemoteMedia()
        .then((rsp) => {
          if (disposed || rsp.code !== 0) return;
          setStatus(rsp.data);
          callback.current(!!rsp.data.connected);
        })
        .catch(() => {});
    refresh();
    const stopPolling = pollWhileVisible(refresh, 2000);
    return () => {
      disposed = true;
      stopPolling();
    };
  }, [isOpen, status.connected, connecting]);
  useEffect(() => {
    if (!connecting && !status.connected) return;
    const warn = (e: BeforeUnloadEvent) => {
      if (socket.current) {
        e.preventDefault();
        e.returnValue = '';
      }
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [connecting, status.connected]);

  function connect() {
    if (!file || socket.current || connecting) return;
    setError('');
    setConnecting(true);
    const selected = file;
    const ws = new WebSocket(`${getBaseUrl('ws')}/api/storage/remote/connect`);
    socket.current = ws;
    let pending = false;
    let receivedError = false;
    let mounted = false;
    ws.onopen = () =>
      ws.send(JSON.stringify({ type: 'open', name: selected.name, size: selected.size }));
    ws.onmessage = async (event) => {
      try {
        const data = JSON.parse(event.data);
        if (data.type === 'error') {
          receivedError = true;
          setError(data.message);
          ws.close();
        } else if (data.type === 'mounted') {
          mounted = true;
          setConnecting(false);
          setStatus({ connected: true, mounted: true, name: selected.name, readBytes: 0 });
          callback.current(true);
          window.dispatchEvent(new Event('nanokvm:image-updated'));
        } else if (data.type === 'read') {
          const { id, offset, length } = data;
          if (
            pending ||
            !Number.isInteger(id) ||
            id < 0 ||
            id > 0xffffffff ||
            !Number.isSafeInteger(offset) ||
            offset < 0 ||
            !Number.isInteger(length) ||
            length < 1 ||
            length > 1048576 ||
            offset > selected.size ||
            length > selected.size - offset
          ) {
            throw new Error(t('image.remote.readFailed'));
          }
          pending = true;
          const block = await selected.slice(offset, offset + length).arrayBuffer();
          if (block.byteLength !== length) throw new Error(t('image.remote.readFailed'));
          const reply = new Uint8Array(4 + length);
          new DataView(reply.buffer).setUint32(0, id, false);
          reply.set(new Uint8Array(block), 4);
          if (ws.readyState === WebSocket.OPEN) ws.send(reply);
          pending = false;
        }
      } catch {
        receivedError = true;
        setError(t('image.remote.readFailed'));
        ws.close();
      }
    };
    ws.onclose = () => {
      if (socket.current !== ws) return;
      socket.current = null;
      setConnecting(false);
      if (!receivedError && !mounted) setError(t('image.remote.connectFailed'));
      window.dispatchEvent(new Event('nanokvm:image-updated'));
    };
  }

  async function connectHTTP() {
    setError('');
    setConnecting(true);
    try {
      const rsp = await api.connectHTTPImage({ url: url.trim(), username, password });
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setStatus({ connected: true, source: 'http', mounted: false });
      callback.current(true);
      window.dispatchEvent(new Event('nanokvm:image-updated'));
    } catch {
      setError(t('image.remote.connectFailed'));
    } finally {
      setPassword('');
      setConnecting(false);
    }
  }

  async function disconnect() {
    setError('');
    try {
      const rsp = await api.disconnectRemoteMedia();
      if (rsp.code !== 0) throw new Error(rsp.msg);
      socket.current?.close();
    } catch {
      setError(t('image.remote.disconnectFailed'));
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-neutral-400">
        {t(
          (status.connected ? status.source : source) === 'http'
            ? 'image.remote.urlHint'
            : 'image.remote.hint'
        )}
      </p>
      {!status.connected && !connecting && source === 'browser' && (
        <input
          type="file"
          accept=".iso"
          aria-label={t('image.remote.select')}
          onChange={(e) => {
            setFile(e.target.files?.[0] ?? null);
            setError('');
          }}
        />
      )}
      {!status.connected && source === 'http' && (
        <div className="flex flex-col gap-3">
          <label className="flex flex-col gap-1">
            <span>{t('image.remote.urlAddress')}</span>
            <Input
              type="url"
              autoComplete="off"
              spellCheck={false}
              placeholder="https://server/image.iso"
              value={url}
              disabled={connecting}
              onChange={(e) => {
                setUrl(e.target.value);
                setError('');
              }}
            />
          </label>
          <span className="text-sm text-neutral-400">{t('image.remote.credentialsHint')}</span>
          <label className="flex flex-col gap-1">
            <span>{t('image.remote.username')}</span>
            <Input
              autoComplete="off"
              value={username}
              disabled={connecting}
              onChange={(e) => setUsername(e.target.value)}
            />
          </label>
          <label className="flex flex-col gap-1">
            <span>{t('image.remote.password')}</span>
            <Input.Password
              autoComplete="new-password"
              value={password}
              disabled={connecting}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
        </div>
      )}
      {(error || status.error) && <Alert type="error" showIcon message={error || status.error} />}
      {status.connected && (
        <div className="flex flex-col gap-1">
          <span className="text-sm text-neutral-400">
            {t(status.source === 'http' ? 'image.remote.url' : 'image.remote.browser')}
          </span>
          <span className="break-all">{status.name}</span>
          <span className="text-sm text-neutral-400">
            {t(status.mounted ? 'image.remote.mounted' : 'image.remote.connecting')}
            {status.mounted &&
              ` · ${(status.readBytes ?? 0) < 1048576 ? `${Math.ceil((status.readBytes ?? 0) / 1024)} KiB` : `${((status.readBytes ?? 0) / 1048576).toFixed(1)} MiB`} ${t('image.remote.read')}`}
          </span>
        </div>
      )}
      {status.connected ? (
        <Button onClick={disconnect}>{t('image.remote.disconnect')}</Button>
      ) : (
        <Button
          type="primary"
          loading={connecting}
          disabled={source === 'http' ? !/^https?:\/\//i.test(url.trim()) : !file}
          onClick={source === 'http' ? connectHTTP : connect}
        >
          {t(connecting ? 'image.remote.connecting' : 'image.remote.connect')}
        </Button>
      )}
    </div>
  );
};
