import { PropsWithChildren, ReactNode, useEffect, useLayoutEffect, useMemo } from 'react';
import { StyleProvider } from '@ant-design/cssinjs';
import { theme as antdTheme, ConfigProvider } from 'antd';
import { useAtomValue } from 'jotai';

import { themeTokens } from '@/lib/theme-tokens';
import { applyTheme } from '@/lib/theme.ts';
import { brandingAtom, brandingButtonColor, buttonTextColor, colorOnLight } from '@/jotai/branding';
import { themeAtom } from '@/jotai/settings.ts';

export const AppTheme = ({ children }: PropsWithChildren) => {
  const branding = useAtomValue(brandingAtom);
  const theme = useAtomValue(themeAtom);
  const buttonColor = brandingButtonColor(branding);
  const colorPrimary = theme === 'light' ? colorOnLight(buttonColor) : buttonColor;
  const themeConfig = useMemo(() => {
    const tokens = themeTokens[theme];
    return {
      algorithm: theme === 'light' ? antdTheme.defaultAlgorithm : antdTheme.darkAlgorithm,
      token: {
        fontFamily: 'var(--font-sans)',
        colorPrimary,
        colorSuccess: tokens.success,
        colorWarning: tokens.warning,
        colorError: tokens.danger,
        colorInfo: tokens.info,
        colorTextDescription: tokens.fgMuted
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
  }, [colorPrimary, theme]);

  // The Tailwind colours follow the attribute on the root element.
  useLayoutEffect(() => applyTheme(theme), [theme]);

  // The accent for plain CSS: antd keeps its own in JavaScript.
  useLayoutEffect(() => {
    document.documentElement.style.setProperty('--color-accent', colorPrimary);
  }, [colorPrimary]);

  // Ant Design mounts static dialogs and messages in separate React roots.
  useEffect(() => {
    ConfigProvider.config({
      holderRender: (node: ReactNode) => (
        <StyleProvider layer>
          <ConfigProvider theme={themeConfig}>{node}</ConfigProvider>
        </StyleProvider>
      )
    });
  }, [themeConfig]);

  return <ConfigProvider theme={themeConfig}>{children}</ConfigProvider>;
};
