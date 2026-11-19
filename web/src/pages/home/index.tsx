import { lazy, Suspense, useEffect, useState } from 'react';
import { Spin } from 'antd';
import { useAtom } from 'jotai';
import { Navigate } from 'react-router-dom';

import { http } from '@/lib/http';
import type { Branding } from '@/jotai/branding';
import { brandingAtom } from '@/jotai/branding';

const Desktop = lazy(() => import('../desktop').then((module) => ({ default: module.Desktop })));

const Loading = () => (
  <div className="flex h-screen items-center justify-center">
    <Spin size="large" />
  </div>
);

// What opens after the sign-in is a setting of the device. The remote screen
// is not mounted before the answer: it would start a video session only to
// drop it. A device that does not answer opens the screen, as it always did.
export const Home = () => {
  const [branding, setBranding] = useAtom(brandingAtom);
  const [failed, setFailed] = useState(false);
  const known = branding.startPage !== undefined;

  useEffect(() => {
    if (known) return;
    let active = true;
    http
      .get<Branding>('/api/branding')
      .then((rsp) => {
        if (!active) return;
        if (rsp.code === 0 && rsp.data.startPage) setBranding(rsp.data);
        else setFailed(true);
      })
      .catch(() => active && setFailed(true));
    return () => {
      active = false;
    };
  }, [known, setBranding]);

  if (!known && !failed) return <Loading />;
  if (branding.startPage === 'overview') return <Navigate to="/overview" replace />;
  return (
    <Suspense fallback={<Loading />}>
      <Desktop />
    </Suspense>
  );
};
