import React, { Suspense } from 'react';
import { StyleProvider } from '@ant-design/cssinjs';
import { Spin } from 'antd';
import ReactDOM from 'react-dom/client';
import { ErrorBoundary } from 'react-error-boundary';
import { RouterProvider } from 'react-router-dom';

import { AppTheme } from './components/app-theme.tsx';
import { MainError } from './components/main-error.tsx';
import { RecheckDialog } from './components/recheck-dialog.tsx';
import { i18nReady } from './i18n';
import { applyTheme, storedTheme } from './lib/theme.ts';
import { router } from './router';

import '@fontsource-variable/inter';
import './assets/styles/index.css';

// before the first paint, the loading spinner included
applyTheme(storedTheme());

const renderApp = () => {
  return ReactDOM.createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
      <Suspense
        fallback={
          <div className="flex h-screen w-screen items-center justify-center">
            <Spin size="large" />
          </div>
        }
      >
        <ErrorBoundary FallbackComponent={MainError}>
          <StyleProvider layer>
            <AppTheme>
              <RouterProvider router={router} />
              <RecheckDialog />
            </AppTheme>
          </StyleProvider>
        </ErrorBoundary>
      </Suspense>
    </React.StrictMode>
  );
};

if (import.meta.env.MODE === 'mocked') {
  const { worker } = await import('./mocks/browser');
  await worker.start();
}

await i18nReady;
renderApp();
