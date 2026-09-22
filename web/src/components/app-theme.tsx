import { PropsWithChildren } from 'react';
import { ConfigProvider, theme } from 'antd';
import { useAtomValue } from 'jotai';

import { brandingAtom, brandingButtonColor, buttonTextColor } from '@/jotai/branding';

export const AppTheme = ({ children }: PropsWithChildren) => {
  const branding = useAtomValue(brandingAtom);
  const colorPrimary = brandingButtonColor(branding);
  const themeConfig = {
    algorithm: theme.darkAlgorithm,
    token: {
      colorPrimary,
      colorSuccess: '#22c55e'
    },
    components: {
      Button: {
        primaryColor: buttonTextColor(colorPrimary)
      },
      Collapse: {
        headerPadding: 0,
        contentPadding: 0
      }
    }
  };

  return <ConfigProvider theme={themeConfig}>{children}</ConfigProvider>;
};
