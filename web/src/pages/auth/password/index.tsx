import { useEffect, useState } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { LockOutlined } from '@ant-design/icons';
import { Button, Card, Form, Input, message } from 'antd';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';

import * as api from '@/api/auth.ts';
import { notifyAuthExpired } from '@/lib/auth-events.ts';
import { encrypt } from '@/lib/encrypt.ts';
import { Head } from '@/components/head.tsx';

// This code is specific to POST /api/auth/password. The backend uses it when
// the authenticated user cannot verify their current password.
const invalidCurrentPasswordCode = -3;
// POST /api/auth/password: the new password was rejected; msg says why.
const invalidNewPasswordCode = -5;

export const Password = () => {
  const { t } = useTranslation();
  const [msg, setMsg] = useState('');
  const navigate = useNavigate();
  const { account } = useAuth();
  const forced = account.mustChangePassword === true;

  useEffect(() => {
    if (msg) {
      setTimeout(() => setMsg(''), 3000);
    }
  }, [msg]);

  function changePassword(values: any) {
    if (values.password !== values.password2) {
      setMsg(t('auth.differentPassword'));
      return;
    }
    const currentPassword = encrypt(values.currentPassword);
    const password = encrypt(values.password);

    api
      .changePassword(currentPassword, password)
      .then((rsp: any) => {
        if (rsp.code !== 0) {
          setMsg(
            rsp.code === invalidCurrentPasswordCode
              ? t('auth.invalidCurrentPassword')
              : rsp.code === invalidNewPasswordCode && rsp.msg
                ? rsp.msg
                : t('auth.error')
          );
          return;
        }

        // The server ended every session of this user; sign in again.
        void message.success(t('auth.passwordChanged'));
        notifyAuthExpired();
        navigate('/auth/login', { replace: true });
      })
      .catch(() => {
        setMsg(t('auth.error'));
      });
  }

  function cancel() {
    // The device stays locked to this page until the factory password changes,
    // so cancelling a forced change logs out (the button says so).
    if (forced) {
      void api
        .logout()
        .catch(() => {
          /* The session is dropped locally either way. */
        })
        .finally(() => {
          notifyAuthExpired();
          navigate('/auth/login', { replace: true });
        });
      return;
    }
    // Return to the previous page without reloading the application.
    if ((window.history.state?.idx ?? 0) > 0) navigate(-1);
    else navigate('/', { replace: true });
  }

  return (
    <>
      <Head title={t('head.changePassword')} />

      <div className="flex h-screen w-screen flex-col items-center justify-center space-y-5">
        <h2 className="text-xl font-semibold text-neutral-100">{t('auth.changePassword')}</h2>
        {forced && (
          <p className="max-w-[450px] text-center text-neutral-300">
            {t('auth.changePasswordDesc')}
          </p>
        )}

        <Form style={{ minWidth: 300, maxWidth: 500 }} onFinish={changePassword}>
          <Form.Item
            name="currentPassword"
            rules={[{ required: true, message: t('auth.noEmptyPassword') }]}
          >
            <Input
              prefix={<LockOutlined />}
              type="password"
              autoComplete="current-password"
              placeholder={t('auth.placeholderCurrentPassword')}
            />
          </Form.Item>

          <Form.Item
            name="password"
            rules={[
              { required: true, message: t('auth.noEmptyPassword') },
              { min: 8, max: 72, message: t('auth.passwordLength') }
            ]}
          >
            <Input
              prefix={<LockOutlined />}
              type="password"
              autoComplete="new-password"
              placeholder={t('auth.placeholderPassword')}
            />
          </Form.Item>

          <Form.Item
            name="password2"
            rules={[
              { required: true, message: t('auth.noEmptyPassword') },
              { min: 8, max: 72, message: t('auth.passwordLength') }
            ]}
          >
            <Input
              prefix={<LockOutlined />}
              type="password"
              autoComplete="new-password"
              placeholder={t('auth.placeholderPassword2')}
            />
          </Form.Item>

          <span className="text-red-500">{msg}</span>
          <Form.Item>
            <div className="flex w-full space-x-2">
              <Button type="primary" htmlType="submit" className="w-1/2">
                {t('auth.ok')}
              </Button>
              <Button className="w-1/2" onClick={cancel}>
                {forced ? t('auth.cancelAndLogout') : t('auth.cancel')}
              </Button>
            </div>
          </Form.Item>
        </Form>

        {account.role === 'admin' && (
          <Card>
            <div className="flex w-[450px] flex-col">
              <div>{t('auth.tips.change1')}</div>
              <ul className="list-outside list-decimal">
                <li>{t('auth.tips.change2')}</li>
                <li>{t('auth.tips.change3')}</li>
              </ul>
              <div className="text-red-500">{t('auth.tips.change4')}</div>
            </div>
          </Card>
        )}
      </div>
    </>
  );
};
