import { useEffect, useRef, useState } from 'react';
import { Button, Divider, InputNumber, message, Modal, Progress, Select } from 'antd';
import { useSetAtom } from 'jotai';
import { ZapIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/hid.ts';
import { keyboardLockAtom } from '@/jotai/keyboard.ts';
import { confirmAction } from '@/components/ui/confirm.ts';

// One-click key sequences the server presses with fixed timing: Ctrl+Alt+Del,
// a Magic SysRq key, and REISUB. The server holds the keyboard for the whole
// sequence, so the timing does not depend on this tab. REISUB runs on after
// its request returns; this modal follows it through the status route.

const sysrqKeys = [
  'h',
  'r',
  'e',
  'i',
  's',
  'u',
  'b',
  'o',
  'k',
  'f',
  'c',
  't',
  'w',
  'm',
  'l'
] as const;

const inputOwnerRequiredCode = -4;
const statusPollMs = 1000;

type Sequence = 'ctrl-alt-del' | 'sysrq' | 'reisub';

type SequenceStatus = {
  running: boolean;
  sequence?: string;
  durationMs?: number;
  remainingMs?: number;
  lastResult?: { sequence: string; ok: boolean; error?: string; finishedAt: string };
};

export const Sequences = () => {
  const { t } = useTranslation();
  const setKeyboardLock = useSetAtom(keyboardLockAtom);

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [sysrqKey, setSysrqKey] = useState<string>('h');
  const [reisubDelay, setReisubDelay] = useState(5);
  const [busy, setBusy] = useState<'' | Sequence>('');
  const [progress, setProgress] = useState<SequenceStatus | null>(null);
  const pollTimer = useRef<number | null>(null);
  // This modal started the running sequence and reports how it ended.
  const following = useRef(false);

  function afterOpenChange(open: boolean) {
    setKeyboardLock({ source: 'key-sequences', locked: open });
    if (open) {
      // A detached REISUB runs on while the modal is closed, and another tab
      // may have started one: ask the server and follow it from here.
      void followSequence();
    } else {
      stopPolling();
    }
  }

  function stopPolling() {
    if (pollTimer.current !== null) {
      window.clearTimeout(pollTimer.current);
      pollTimer.current = null;
    }
  }

  useEffect(() => stopPolling, []);

  // Follow a detached sequence until the server says it ended. Only the
  // modal that started it announces the result.
  async function followSequence() {
    stopPolling();
    try {
      const rsp = await api.getKeySequenceStatus();
      if (rsp.code !== 0) return;
      const status = rsp.data as SequenceStatus;
      if (status.running) {
        setProgress(status);
        setBusy((status.sequence as Sequence | undefined) ?? 'reisub');
        pollTimer.current = window.setTimeout(followSequence, statusPollMs);
        return;
      }
      setProgress(null);
      setBusy('');
      if (!following.current) return;
      following.current = false;
      if (status.lastResult?.ok) {
        void message.success(t('keyboard.sequences.sent'));
      } else {
        void message.error(t('keyboard.sequences.failed'));
      }
    } catch {
      following.current = false;
      setBusy('');
      setProgress(null);
    }
  }

  async function send(sequence: Sequence, options: { key?: string; delayMs?: number } = {}) {
    setBusy(sequence);
    setProgress(null);
    try {
      const rsp = await api.sendKeySequence(sequence, options);
      if (rsp.code === inputOwnerRequiredCode) {
        void message.error(t('keyboard.sequences.notController'));
      } else if (rsp.code !== 0) {
        void message.error(t('keyboard.sequences.failed'));
      } else if (sequence === 'reisub') {
        // the request returned at once; the steps run on
        setProgress({
          running: true,
          sequence,
          durationMs: rsp.data?.durationMs,
          remainingMs: rsp.data?.durationMs
        });
        following.current = true;
        stopPolling();
        pollTimer.current = window.setTimeout(followSequence, statusPollMs);
        return;
      } else {
        void message.success(t('keyboard.sequences.sent'));
      }
    } catch {
      void message.error(t('keyboard.sequences.failed'));
    }
    setBusy('');
  }

  async function sendReisub() {
    const ok = await confirmAction({
      title: t('keyboard.sequences.reisub.confirmTitle'),
      content: t('keyboard.sequences.reisub.confirm', { seconds: reisubDelay }),
      okText: t('keyboard.sequences.reisub.send'),
      danger: true
    });
    if (!ok) return;
    await send('reisub', { delayMs: reisubDelay * 1000 });
  }

  const sysrqOptions = sysrqKeys.map((key) => ({
    value: key,
    label: `${key.toUpperCase()} — ${t(`keyboard.sequences.sysrq.keys.${key}`)}`
  }));

  const reisubRunning = progress?.running && progress.sequence === 'reisub';
  const reisubPercent =
    reisubRunning && progress.durationMs
      ? Math.min(100, Math.round(100 - ((progress.remainingMs ?? 0) / progress.durationMs) * 100))
      : 0;

  return (
    <>
      <div
        className="flex cursor-pointer items-center space-x-2 rounded py-1 pr-5 pl-2 select-none hover:bg-neutral-700/70"
        onClick={() => setIsModalOpen(true)}
      >
        <ZapIcon size={18} />
        <span>{t('keyboard.sequences.title')}</span>
      </div>

      <Modal
        open={isModalOpen}
        centered={false}
        footer={null}
        onCancel={() => setIsModalOpen(false)}
        afterOpenChange={afterOpenChange}
      >
        <div className="flex flex-col">
          <span className="text-xl">{t('keyboard.sequences.title')}</span>
          <span className="text-sm text-neutral-600">{t('keyboard.sequences.tips')}</span>
        </div>

        <Divider style={{ margin: '14px 0' }} />

        <div className="flex items-center justify-between space-x-3">
          <div className="flex flex-col">
            <span>Ctrl + Alt + Del</span>
            <span className="text-xs text-neutral-500">{t('keyboard.sequences.ctrlAltDel')}</span>
          </div>
          <Button
            loading={busy === 'ctrl-alt-del'}
            disabled={busy !== ''}
            onClick={() => send('ctrl-alt-del')}
          >
            {t('keyboard.sequences.send')}
          </Button>
        </div>

        <Divider style={{ margin: '14px 0' }} />

        <div className="flex flex-col space-y-2">
          <div className="flex flex-col">
            <span>{t('keyboard.sequences.sysrq.title')}</span>
            <span className="text-xs text-neutral-500">{t('keyboard.sequences.sysrq.desc')}</span>
          </div>
          <div className="flex items-center space-x-3">
            <Select
              className="flex-1"
              variant="filled"
              value={sysrqKey}
              onChange={(value) => setSysrqKey(value)}
              options={sysrqOptions}
            />
            <Button
              loading={busy === 'sysrq'}
              disabled={busy !== ''}
              onClick={() => send('sysrq', { key: sysrqKey })}
            >
              {t('keyboard.sequences.send')}
            </Button>
          </div>
        </div>

        <Divider style={{ margin: '14px 0' }} />

        <div className="flex flex-col space-y-2">
          <div className="flex flex-col">
            <span>{t('keyboard.sequences.reisub.title')}</span>
            <span className="text-xs text-neutral-500">{t('keyboard.sequences.reisub.desc')}</span>
          </div>
          <div className="flex items-center space-x-3">
            <span className="text-sm text-neutral-400">{t('keyboard.sequences.reisub.delay')}</span>
            <InputNumber
              min={1}
              max={30}
              value={reisubDelay}
              onChange={(value) => value !== null && setReisubDelay(value)}
              addonAfter="s"
              style={{ width: 110 }}
              disabled={busy !== ''}
            />
            <div className="flex-1" />
            <Button danger loading={busy === 'reisub'} disabled={busy !== ''} onClick={sendReisub}>
              {t('keyboard.sequences.reisub.send')}
            </Button>
          </div>
          {reisubRunning && (
            <div className="flex items-center space-x-3">
              <Progress percent={reisubPercent} size="small" showInfo={false} className="flex-1" />
              <span className="text-xs text-neutral-400">
                {t('keyboard.sequences.reisub.running', {
                  seconds: Math.ceil((progress?.remainingMs ?? 0) / 1000)
                })}
              </span>
            </div>
          )}
        </div>
      </Modal>
    </>
  );
};
