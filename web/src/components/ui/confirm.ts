import { ReactNode } from 'react';
import { Modal } from 'antd';

type ConfirmOptions = {
  title: ReactNode;
  content?: ReactNode;
  okText?: string;
  cancelText?: string;
  // Red confirm button for actions that cut a connection or risk the device.
  danger?: boolean;
};

// Ask before a change that interrupts the connection or risks the device.
// Resolves to true when confirmed. Static Modal follows the app theme
// (components/app-theme.tsx).
export const confirmAction = ({ title, content, okText, cancelText, danger }: ConfirmOptions) =>
  new Promise<boolean>((resolve) => {
    Modal.confirm({
      title,
      content,
      okText,
      cancelText,
      okButtonProps: danger ? { danger: true } : undefined,
      onOk: () => resolve(true),
      onCancel: () => resolve(false)
    });
  });
