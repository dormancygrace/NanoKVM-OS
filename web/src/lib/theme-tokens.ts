// Semantic colours of the two themes, for the antd theme
// (components/app-theme.tsx). The Tailwind utilities have the same values:
// the dark ones in @theme of assets/styles/index.css, the light ones in
// assets/styles/light.css. In components use the utilities (text-fg,
// border-line, ...) or var(--color-...), which follow the theme by themselves.
export type Theme = 'dark' | 'light';

export const themeTokens = {
  dark: {
    fgMuted: '#a3a3a3', // descriptions, captions; lowest contrast for information
    success: '#22c55e',
    warning: '#fbbf24',
    danger: '#f87171',
    info: '#38bdf8'
  },
  light: {
    fgMuted: '#404040',
    success: '#15803d',
    warning: '#b45309',
    danger: '#dc2626',
    info: '#0369a1'
  }
} as const;
