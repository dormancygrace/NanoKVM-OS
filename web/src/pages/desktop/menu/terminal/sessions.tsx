import { Divider, Popconfirm } from 'antd';
import { ExternalLinkIcon, XIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { closeTerminalSession, type TerminalSession } from '@/api/terminal.ts';
import { openShellTerminal } from '@/pages/terminal/launch.ts';

type SessionsProps = {
  sessions: TerminalSession[];
  max: number;
  onChange: () => void;
};

function browserName(userAgent: string) {
  for (const [token, name] of [
    ['Edg/', 'Edge'],
    ['OPR/', 'Opera'],
    ['Firefox/', 'Firefox'],
    ['Chrome/', 'Chrome'],
    ['Safari/', 'Safari']
  ]) {
    if (userAgent.includes(token)) return name;
  }
  return '';
}

// The running shells: what each one runs, who started it, and whether a page
// is attached or how long it waits for one. Own shells can be opened again;
// any can be ended.
export const Sessions = ({ sessions, max, onChange }: SessionsProps) => {
  const { t, i18n } = useTranslation();
  if (sessions.length === 0) return null;

  const relative = new Intl.RelativeTimeFormat(i18n.language, { numeric: 'auto' });
  const minutes = (ms: number) => Math.round(ms / 60000);

  async function close(id: string) {
    await closeTerminalSession(id).catch(() => undefined);
    onChange();
  }

  return (
    <>
      <Divider style={{ margin: '10px 0' }} />
      <div className="px-2 pb-1 text-xs text-neutral-400">
        {t('terminal.sessions', { count: sessions.length, max })}
      </div>
      {sessions.map((session) => {
        const where = [session.clientIp, browserName(session.userAgent)].filter(Boolean).join(', ');
        const state = session.attached
          ? t('terminal.sessionAttached', { where })
          : t('terminal.sessionDetached', {
              ends: relative.format(minutes((session.endsAt ?? 0) - Date.now()), 'minute')
            });
        return (
          <div
            key={session.id}
            className="flex items-center justify-between space-x-2 rounded px-2 py-1 hover:bg-neutral-700/70"
          >
            <div className="flex min-w-0 flex-col">
              <span className="truncate font-mono text-sm">{session.command || 'sh'}</span>
              <span className="truncate text-xs text-neutral-400">
                {session.username} ·{' '}
                {relative.format(minutes(session.createdAt - Date.now()), 'minute')} · {state}
              </span>
            </div>
            <div className="flex shrink-0 items-center space-x-1">
              {session.own && (
                <button
                  type="button"
                  className="flex h-6 w-6 items-center justify-center rounded border-0 bg-transparent text-inherit hover:bg-neutral-600"
                  title={t('terminal.sessionOpen')}
                  aria-label={t('terminal.sessionOpen')}
                  onClick={() => openShellTerminal(session.id)}
                >
                  <ExternalLinkIcon size={14} />
                </button>
              )}
              <Popconfirm
                title={t('terminal.sessionCloseConfirm')}
                okText={t('terminal.sessionClose')}
                cancelText={t('terminal.cancel')}
                okButtonProps={{ danger: true }}
                onConfirm={() => close(session.id)}
              >
                <button
                  type="button"
                  className="flex h-6 w-6 items-center justify-center rounded border-0 bg-transparent text-inherit hover:bg-neutral-600"
                  title={t('terminal.sessionClose')}
                  aria-label={t('terminal.sessionClose')}
                >
                  <XIcon size={14} />
                </button>
              </Popconfirm>
            </div>
          </div>
        );
      })}
    </>
  );
};
