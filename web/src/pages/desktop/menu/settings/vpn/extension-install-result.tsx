import type { ReactNode } from 'react';
import { Button, Result } from 'antd';
import { DownloadIcon } from 'lucide-react';

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
        <Button type="primary" loading={loading} onClick={onInstall}>
          {actionLabel}
        </Button>
      }
    />
  );
}
