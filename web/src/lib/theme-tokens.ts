// Semantic colours, shared by the antd theme (components/app-theme.tsx) and
// the Tailwind utilities (@theme in assets/styles/index.css; a test keeps the
// two equal). Use these instead of raw neutral/green/amber/red classes.
export const themeTokens = {
  fg: '#e5e5e5', // body text (neutral-200)
  fgMuted: '#a3a3a3', // descriptions, captions; lowest contrast for information (neutral-400)
  line: 'rgb(64 64 64 / 0.7)', // borders and dividers (neutral-700/70)
  surface: 'rgb(38 38 38 / 0.4)', // cards and panels (neutral-800/40)
  surfaceRaised: 'rgb(38 38 38 / 0.7)', // rows and hover (neutral-800/70)
  success: '#22c55e',
  warning: '#fbbf24',
  danger: '#f87171',
  info: '#38bdf8'
} as const;

export type ThemeToken = keyof typeof themeTokens;
