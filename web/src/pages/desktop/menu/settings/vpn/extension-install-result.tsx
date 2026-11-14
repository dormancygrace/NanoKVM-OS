import type { ReactNode } from 'react';
import { Button, Result } from 'antd';
import { DownloadIcon } from 'lucide-react';

import '../../download-progress.css';

type ExtensionInstallResultProps = {
  title: ReactNode;
  description: ReactNode;
  actionLabel: ReactNode;
  loading?: boolean;
  onInstall: () => void;
};

export function ExtensionInstallResult({
  title,
  description,
  actionLabel,
  loading = false,
  onInstall
}: ExtensionInstallResultProps) {
  return (
    <Result
      status="info"
      icon={<DownloadIcon size={28} />}
      title={title}
      subTitle={description}
      extra={
        <div
          className="mx-auto flex w-full max-w-sm flex-col items-center gap-4"
          aria-busy={loading}
        >
          <Button type="primary" loading={loading} onClick={onInstall}>
            {actionLabel}
          </Button>
          {loading && (
            <div
              role="progressbar"
              aria-label={typeof actionLabel === 'string' ? actionLabel : undefined}
              className="bg-surface-raised h-2 w-full overflow-hidden rounded-full"
            >
              <div className="download-progress-fill download-indeterminate" />
            </div>
          )}
        </div>
      }
    />
  );
}
