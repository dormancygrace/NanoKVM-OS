import { ReactElement, useEffect, useRef, useState } from 'react';
import { Button, Form, Input } from 'antd';
import { useAtomValue } from 'jotai';
import { KeyRoundIcon, LockIcon, UserIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';

import * as api from '@/api/auth.ts';
import { brandingAtom, brandingLogo } from '@/jotai/branding';
import { Head } from '@/components/head.tsx';

import { Tips } from './tips.tsx';

export const Login = (): ReactElement => {
  const branding = useAtomValue(brandingAtom);
  const navigate = useNavigate();
  const { t } = useTranslation();

  const [isLoading, setIsloading] = useState(false);
  const [msg, setMsg] = useState('');
  const logoRef = useRef<HTMLImageElement>(null);

  useEffect(() => {
    api
      .getAccount()
      .then((rsp) => {
        if (rsp.code === 0 && rsp.data?.username) {
          navigate('/', { replace: true });
        }
      })
      .catch(() => {});
  }, [navigate]);

  useEffect(() => {
    if (msg) {
      setTimeout(() => setMsg(''), 3000);
    }
  }, [msg]);

  // The login waiting for the second factor.
  const [challenge, setChallenge] = useState('');

  function failure(code: number, secondFactor: boolean) {
    if (code === -2) return t(secondFactor ? 'auth.totp.invalidCode' : 'auth.invalidUser');
    if (code === -5) return t('auth.locked');
    if (code === -4) return t('auth.globalLocked');
    if (code === api.TOTP_CLOCK_NOT_SET) return t('auth.totp.clockNotSet');
    return t('auth.error');
  }

  function login(values: any) {
    if (isLoading) return;
    setIsloading(true);

    const username = values.username;
    const password = values.password;

    api
      .login(username, password)
      .then((rsp) => {
        if (rsp.code === api.LOGIN_TOTP_REQUIRED && rsp.data && 'challenge' in rsp.data) {
          setMsg('');
          setChallenge(rsp.data.challenge);
          return;
        }
        if (rsp.code !== 0) {
          setMsg(failure(rsp.code, false));
          return;
        }

        setMsg('');
        navigate('/', { replace: true });
      })
      .catch(() => {
        setMsg(t('auth.error'));
      })
      .finally(() => {
        setIsloading(false);
      });
  }

  function loginTOTP(values: { otp: string }) {
    if (isLoading) return;
    setIsloading(true);
    api
      .loginTOTP(challenge, values.otp)
      .then((rsp) => {
        if (rsp.code === api.LOGIN_EXPIRED) {
          setChallenge('');
          setMsg(t('auth.totp.expired'));
          return;
        }
        if (rsp.code !== 0) {
          setMsg(failure(rsp.code, true));
          return;
        }
        setMsg('');
        navigate('/', { replace: true });
      })
      .catch(() => setMsg(t('auth.error')))
      .finally(() => setIsloading(false));
  }

  function spinLogo() {
    const logo = logoRef.current;
    if (!logo) return;
    logo.classList.add('animate-spin');
    setTimeout(() => logo.classList.remove('animate-spin'), 1000);
  }

  return (
    <>
      <Head title={t('head.login')} />

      <div className="flex h-screen w-screen flex-col items-center justify-center">
        {challenge ? (
          <Form<{ otp: string }>
            key="totp"
            style={{ minWidth: 300, maxWidth: 500 }}
            layout="vertical"
            onFinish={loginTOTP}
          >
            <h2 className="mb-1 text-lg font-medium">{t('auth.totp.title')}</h2>
            <p className="text-fg-muted mb-4 text-sm">{t('auth.totp.prompt')}</p>
            <Form.Item name="otp" rules={[{ required: true, message: t('auth.totp.required') }]}>
              <Input
                autoFocus
                prefix={<KeyRoundIcon size={14} />}
                autoComplete="one-time-code"
                inputMode="text"
                maxLength={16}
                placeholder={t('auth.totp.placeholder')}
              />
            </Form.Item>
            <div className="pb-1 text-red-500">{msg}</div>
            <Form.Item>
              <Button type="primary" htmlType="submit" className="w-full" loading={isLoading}>
                {t('auth.totp.verify')}
              </Button>
            </Form.Item>
            <Button
              type="link"
              className="px-0"
              onClick={() => {
                setChallenge('');
                setMsg('');
              }}
            >
              {t('auth.totp.back')}
            </Button>
          </Form>
        ) : (
          <Form style={{ minWidth: 300, maxWidth: 500 }} onFinish={login}>
            <div className="flex flex-col items-center justify-center pb-4">
              <button type="button" className="nanokvm-button-base max-w-full" onClick={spinLogo}>
                <img
                  ref={logoRef}
                  id="logo"
                  src={brandingLogo(branding)}
                  width={300}
                  height={branding.customLogoAvailable ? 100 : 180}
                  alt={branding.customLogoAvailable ? 'Logo' : 'NanoKVM OS'}
                  className="block max-w-full object-contain"
                />
              </button>
            </div>
            <Form.Item
              name="username"
              rules={[{ required: true, message: t('auth.noEmptyUsername'), min: 1 }]}
            >
              <Input prefix={<UserIcon size={14} />} placeholder={t('auth.placeholderUsername')} />
            </Form.Item>

            <Form.Item
              name="password"
              rules={[{ required: true, message: t('auth.noEmptyPassword'), min: 1 }]}
            >
              <Input
                prefix={<LockIcon size={14} />}
                type="password"
                placeholder={t('auth.placeholderPassword')}
              />
            </Form.Item>

            <div className="pb-1 text-red-500">{msg}</div>

            <Form.Item>
              <Button type="primary" htmlType="submit" className="w-full" loading={isLoading}>
                {t('auth.loginButtonText')}
              </Button>
            </Form.Item>

            <div className="flex justify-end pb-4 text-sm">
              <Tips />
            </div>
          </Form>
        )}
      </div>
    </>
  );
};
