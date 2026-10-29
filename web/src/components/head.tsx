import { useEffect } from 'react';
import { useOptionalAuth } from '@/contexts/auth.ts';
import { useAtom } from 'jotai';

import { getWebTitle } from '@/api/vm.ts';
import { http } from '@/lib/http';
import type { Branding } from '@/jotai/branding';
import { brandingAtom, brandingFavicon } from '@/jotai/branding';
import { webTitleAtom } from '@/jotai/settings.ts';

type HeadProps = {
  title?: string;
  description?: string;
};

export const Head = ({ title = '', description = '' }: HeadProps = {}) => {
  const [webTitle, setWebTitle] = useAtom(webTitleAtom);
  const auth = useOptionalAuth();
  const [branding, setBranding] = useAtom(brandingAtom);
  useEffect(() => {
    http
      .get<Branding>('/api/branding')
      .then((rsp) => {
        if (rsp.code === 0) {
          setBranding(rsp.data);
          if (rsp.data.title) setWebTitle(rsp.data.title);
        }
      })
      .catch(() => {});
  }, [setBranding, setWebTitle]);

  useEffect(() => {
    if (!auth) return;

    getWebTitle().then((rsp) => {
      if (rsp.data?.title) {
        setWebTitle(rsp.data.title);
      }
    });
  }, [auth, setWebTitle]);

  return (
    <>
      <title>{webTitle || (title ? `${title} - NanoKVM OS` : 'NanoKVM OS')}</title>
      <meta name="description" content={description} />
      <link
        rel="icon"
        type={branding.customFaviconAvailable ? 'image/png' : 'image/svg+xml'}
        href={brandingFavicon(branding)}
      />
    </>
  );
};
