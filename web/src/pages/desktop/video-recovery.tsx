import { useState } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { Alert, Button, message, Modal } from 'antd';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http.ts';

export function VideoRecovery({ retrying }: { retrying: boolean }) {
  const { account } = useAuth();
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [reports, setReports] = useState<string | null>(null);
  const showReports = async () => {
    try {
      const rsp = await http.get('/api/vm/video/crashes');
      if (rsp.code !== 0) throw new Error('crash reports unavailable');
      setReports(JSON.stringify(rsp.data, null, 2));
    } catch {
      message.error(t('screen.recoveryReportsFailed'));
    }
  };
  const retry = async () => {
    setBusy(true);
    try {
      const rsp = await http.post('/api/vm/video/retry');
      if (rsp.code !== 0) throw new Error('retry failed');
      // The server replaces itself after acknowledging; reconnect from a
      // clean page so the previously refused stream is mounted again.
      window.setTimeout(() => window.location.reload(), 3000);
    } catch {
      message.error(t('screen.recoveryRetryFailed'));
      setBusy(false);
    }
  };
  return (
    <>
      <Alert
        className="fixed top-16 left-1/2 z-990 w-[min(90%,600px)] -translate-x-1/2"
        type="warning"
        showIcon
        message={t('screen.recoveryTitle')}
        description={t('screen.recoveryDescription')}
        action={
          account.role === 'admin' && (
            <div className="flex flex-col gap-2">
              <Button loading={busy || retrying} onClick={() => void retry()}>
                {t('screen.recoveryRetry')}
              </Button>
              <Button onClick={() => void showReports()}>{t('screen.recoveryReports')}</Button>
            </div>
          )
        }
      />
      <Modal
        open={reports !== null}
        title={t('screen.recoveryReports')}
        footer={null}
        onCancel={() => setReports(null)}
        width={800}
      >
        <pre className="max-h-[60vh] overflow-auto text-xs break-all whitespace-pre-wrap">
          {reports}
        </pre>
      </Modal>
    </>
  );
}
