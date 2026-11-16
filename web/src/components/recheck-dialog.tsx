import { useEffect, useRef, useState } from 'react';
import { Alert, Input, Modal } from 'antd';
import { useTranslation } from 'react-i18next';

import { getRecheck, recheck } from '@/api/auth.ts';
import { RECHECK_EVENT, type RecheckRequest } from '@/lib/recheck.ts';

/*
 * The password (and second-factor) confirmation that actions acting as root
 * ask for: a shell, scripts, scheduled commands, accounts, SSH and the TLS
 * certificate. Opened by requestRecheck(); mounted once for the whole app.
 */
export const RecheckDialog = () => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [totp, setTotp] = useState(false);
  const [password, setPassword] = useState('');
  const [otp, setOtp] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const waiting = useRef<((confirmed: boolean) => void)[]>([]);

  useEffect(() => {
    const onRequest = (event: Event) => {
      const { resolve } = (event as CustomEvent<RecheckRequest>).detail;
      waiting.current.push(resolve);
      setPassword('');
      setOtp('');
      setError('');
      setOpen(true);
      void getRecheck()
        .then((rsp) => {
          if (rsp.code === 0) setTotp(rsp.data.totp);
        })
        .catch(() => undefined);
    };
    window.addEventListener(RECHECK_EVENT, onRequest);
    return () => window.removeEventListener(RECHECK_EVENT, onRequest);
  }, []);

  const finish = (confirmed: boolean) => {
    const resolves = waiting.current;
    waiting.current = [];
    setOpen(false);
    setPassword('');
    setOtp('');
    resolves.forEach((resolve) => resolve(confirmed));
  };

  const submit = async () => {
    if (!password || (totp && !otp) || busy) return;
    setBusy(true);
    setError('');
    try {
      const rsp = await recheck(password, otp);
      if (rsp.code === 0) {
        finish(true);
      } else {
        setError(rsp.msg || t('recheck.failed'));
      }
    } catch {
      setError(t('recheck.failed'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      title={t('recheck.title')}
      okText={t('recheck.confirm')}
      okButtonProps={{ disabled: !password || (totp && !otp), loading: busy }}
      onOk={() => void submit()}
      onCancel={() => finish(false)}
      destroyOnHidden
    >
      <div className="flex flex-col gap-3">
        <p className="text-fg-muted m-0 text-sm">{t('recheck.description')}</p>
        <Input.Password
          autoFocus
          autoComplete="current-password"
          placeholder={t('recheck.password')}
          aria-label={t('recheck.password')}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          onPressEnter={() => void submit()}
        />
        {totp && (
          <Input
            inputMode="numeric"
            autoComplete="one-time-code"
            placeholder={t('recheck.code')}
            aria-label={t('recheck.code')}
            value={otp}
            onChange={(e) => setOtp(e.target.value.trim())}
            onPressEnter={() => void submit()}
          />
        )}
        {error && <Alert type="error" showIcon title={error} />}
      </div>
    </Modal>
  );
};
