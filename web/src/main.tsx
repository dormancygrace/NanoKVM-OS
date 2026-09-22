import React, { Suspense } from 'react';
import { StyleProvider } from '@ant-design/cssinjs';
import { Spin } from 'antd';
import ReactDOM from 'react-dom/client';
import { ErrorBoundary } from 'react-error-boundary';
import { HelmetProvider } from 'react-helmet-async';
import { RouterProvider } from 'react-router-dom';

import { AppTheme } from './components/app-theme.tsx';
import { MainError } from './components/main-error.tsx';
import { router } from './router';

import './i18n';
import './assets/styles/index.css';

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
          <HelmetProvider>
            <StyleProvider layer>
              <AppTheme>
                <RouterProvider router={router} />
              </AppTheme>
            </StyleProvider>
          </HelmetProvider>
        </ErrorBoundary>
      </Suspense>
    </React.StrictMode>
  );
};

if (import.meta.env.MODE === 'mocked') {
  const { worker } = await import('./mocks/browser');
  worker.start().then(() => {
    return renderApp();
  });
}

renderApp();
