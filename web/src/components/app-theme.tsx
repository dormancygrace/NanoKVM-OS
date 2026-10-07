import { PropsWithChildren, ReactNode, useEffect, useMemo } from 'react';
import { ConfigProvider, theme } from 'antd';
import { useAtomValue } from 'jotai';

import { themeTokens } from '@/lib/theme-tokens';
import { brandingAtom, brandingButtonColor, buttonTextColor } from '@/jotai/branding';

export const AppTheme = ({ children }: PropsWithChildren) => {
  const branding = useAtomValue(brandingAtom);
  const colorPrimary = brandingButtonColor(branding);
  const themeConfig = useMemo(
    () => ({
      algorithm: theme.darkAlgorithm,
      token: {
        colorPrimary,
        colorSuccess: themeTokens.success,
        colorWarning: themeTokens.warning,
        colorError: themeTokens.danger,
        colorInfo: themeTokens.info,
        colorTextDescription: themeTokens.fgMuted
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
    }),
    [colorPrimary]
  );

  // Static message, Modal.confirm and notification calls render outside this
  // provider; give them the same dark theme and colours.
  useEffect(() => {
    ConfigProvider.config({
      holderRender: (node: ReactNode) => <ConfigProvider theme={themeConfig}>{node}</ConfigProvider>
    });
  }, [themeConfig]);

  return <ConfigProvider theme={themeConfig}>{children}</ConfigProvider>;
};
