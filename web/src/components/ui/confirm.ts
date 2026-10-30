import { ReactNode } from 'react';
import { Modal } from 'antd';

type ConfirmOptions = {
  title: ReactNode;
  content?: ReactNode;
  okText?: string;
  cancelText?: string;
  // Red confirm button for actions that cut a connection or risk the device.
  danger?: boolean;
  // Cancel is the highlighted and focused button, so Enter keeps things as
  // they are: for actions people reach by mistake, such as signing out.
  safeDefault?: boolean;
};

// Ask before a change that interrupts the connection or risks the device.
// Resolves to true when confirmed. Static Modal follows the app theme
// (components/app-theme.tsx).
export const confirmAction = ({
  title,
  content,
  okText,
  cancelText,
  danger,
  safeDefault
}: ConfirmOptions) =>
  new Promise<boolean>((resolve) => {
    Modal.confirm({
      title,
      content,
      okText,
      cancelText,
      okButtonProps: { danger, type: safeDefault ? 'default' : 'primary' },
      cancelButtonProps: safeDefault ? { type: 'primary' } : undefined,
      focusable: { autoFocusButton: safeDefault ? 'cancel' : 'ok' },
      onOk: () => resolve(true),
      onCancel: () => resolve(false)
    });
  });
