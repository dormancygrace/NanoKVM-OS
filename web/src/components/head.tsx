import { useEffect } from 'react';
import { useOptionalAuth } from '@/contexts/auth.ts';
import { useAtom } from 'jotai';
import { Helmet, HelmetData } from 'react-helmet-async';

import { getWebTitle } from '@/api/vm.ts';
import { http } from '@/lib/http';
import { brandingAtom, brandingLogo } from '@/jotai/branding';
import { webTitleAtom } from '@/jotai/settings.ts';

type HeadProps = {
  title?: string;
  description?: string;
};

const helmetData = new HelmetData({});

export const Head = ({ title = '', description = '' }: HeadProps = {}) => {
  const [webTitle, setWebTitle] = useAtom(webTitleAtom);
  const auth = useOptionalAuth();
  const [branding, setBranding] = useAtom(brandingAtom);
  useEffect(() => {
    http
      .get('/api/branding')
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
    <Helmet
      helmetData={helmetData}
      title={webTitle ? webTitle : title ? `${title} - NanoKVM OS` : 'NanoKVM OS'}
      defaultTitle={webTitle}
    >
      <meta name="description" content={description} />
      <link
        rel="icon"
        type={branding.style === 'custom' ? 'image/png' : 'image/svg+xml'}
        href={brandingLogo(branding)}
      />
    </Helmet>
  );
};
