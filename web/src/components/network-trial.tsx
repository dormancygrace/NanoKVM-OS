import { useEffect, useState } from 'react';
import { Alert, Button, Modal, Spin } from 'antd';
import { useTranslation } from 'react-i18next';

import { confirmNetworkTrial } from '@/api/network.ts';
import {
  finishNetworkTrial,
  forgetNetworkTrial,
  networkConfirmURL,
  networkTrialStarted,
  originOf,
  pendingNetworkTrial,
  probeTrial,
  type NetworkTrialResult,
  type PendingTrial
} from '@/lib/network-trial.ts';

type State = 'waiting' | 'moving' | NetworkTrialResult;

const PROBE_INTERVAL = 2000;
const CONFIRM_TIMEOUT = 5000;
const MISSES_BEFORE_MOVING = 3;

// After a change that can cut the device off, looks for it: here first, then
// at the addresses of the trial. Reached here, the change is kept; reached
// elsewhere, the browser moves there and the page there keeps it. Mounted
// once for the whole app, so the check survives closing the settings and a
// reload.
export const NetworkTrialHost = () => {
  const { t } = useTranslation();
  const [pending, setPending] = useState<PendingTrial | null>(() => pendingNetworkTrial());
  const [state, setState] = useState<State>('waiting');
  const [remaining, setRemaining] = useState(0);
  const [found, setFound] = useState('');

  useEffect(() => {
    const started = (event: Event) => setPending((event as CustomEvent<PendingTrial>).detail);
    window.addEventListener(networkTrialStarted, started);
    return () => window.removeEventListener(networkTrialStarted, started);
  }, []);

  useEffect(() => {
    if (!pending) return;
    const { trial, deadline } = pending;
    setState('waiting');
    const here = window.location.hostname;
    const hosts = [here, ...trial.hosts.filter((host) => host !== here)];
    let stopped = false;
    let busy = false;
    // This address is preferred: the interface restarts with the change, so
    // it may miss a round or two before it answers again.
    let missedHere = 0;
    const finish = (result: State) => {
      stopped = true;
      if (result !== 'moving') forgetNetworkTrial();
      setState(result);
    };
    const tick = async () => {
      if (stopped) return;
      const left = Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
      setRemaining(left);
      if (left === 0) {
        finish('reverted');
        return;
      }
      if (busy) return;
      busy = true;
      try {
        for (const host of hosts) {
          const answer = await probeTrial(host, trial.id);
          if (stopped) return;
          if (answer === 'ended') {
            // Kept by a confirmation whose answer got lost, from another
            // tab, or undone early: the settings shown next tell which.
            finish('ended');
            return;
          }
          if (answer !== 'applied') {
            // Only not answering counts; before the restart it still answers.
            if (host === here) missedHere = answer === null ? missedHere + 1 : 0;
            continue;
          }
          if (host !== here && missedHere < MISSES_BEFORE_MOVING) return;
          if (host === here) {
            try {
              const rsp = await confirmNetworkTrial(trial.token, CONFIRM_TIMEOUT);
              if (!stopped && rsp.code === 0) finish('kept');
            } catch {
              // The next round asks the device again.
            }
          } else {
            finish('moving');
            setFound(host);
            window.location.assign(networkConfirmURL(host, trial.token));
          }
          return;
        }
      } finally {
        busy = false;
      }
    };
    void tick();
    const timer = setInterval(tick, PROBE_INTERVAL);
    return () => {
      stopped = true;
      clearInterval(timer);
    };
  }, [pending]);

  const done = state === 'kept' || state === 'reverted' || state === 'ended';
  const close = () => {
    if (!done) return;
    setPending(null);
    finishNetworkTrial(state);
  };
  return (
    <Modal
      open={!!pending}
      title={t('settings.network.trial.title')}
      closable={done}
      maskClosable={false}
      keyboard={done}
      onCancel={close}
      footer={
        done ? (
          <Button type="primary" onClick={close}>
            {t('settings.network.trial.close')}
          </Button>
        ) : null
      }
    >
      {state === 'waiting' && pending && (
        <div className="space-y-3">
          <div className="flex items-center gap-3">
            <Spin size="small" />
            <span>{t('settings.network.trial.waiting', { seconds: remaining })}</span>
          </div>
          <div className="text-xs text-neutral-500">{t('settings.network.trial.manual')}</div>
          <ul className="space-y-1 text-xs">
            {/* A new tab: this one keeps looking, and a self-signed
                certificate can be accepted there first. */}
            {pending.trial.hosts.map((host) => (
              <li key={host}>
                <a
                  href={networkConfirmURL(host, pending.trial.token)}
                  target="_blank"
                  rel="noreferrer"
                >
                  {originOf(host)}
                </a>
              </li>
            ))}
          </ul>
          {window.location.protocol === 'https:' && (
            <div className="text-xs text-neutral-500">{t('settings.network.trial.https')}</div>
          )}
        </div>
      )}
      {state === 'moving' && (
        <div className="flex items-center gap-3">
          <Spin size="small" />
          <span>{t('settings.network.trial.moving', { host: found })}</span>
        </div>
      )}
      {state === 'kept' && (
        <Alert type="success" showIcon message={t('settings.network.trial.kept')} />
      )}
      {state === 'ended' && (
        <Alert type="info" showIcon message={t('settings.network.trial.ended')} />
      )}
      {state === 'reverted' && (
        <Alert type="warning" showIcon message={t('settings.network.trial.reverted')} />
      )}
    </Modal>
  );
};
