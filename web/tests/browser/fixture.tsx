import { StrictMode } from 'react';
import i18n, { i18nReady } from '@/i18n';
import { StyleProvider } from '@ant-design/cssinjs';
import { createRoot } from 'react-dom/client';
import { createHashRouter, RouterProvider } from 'react-router-dom';

import { AppTheme } from '@/components/app-theme';
import { ProtectedRoute } from '@/components/auth';
import { Login } from '@/pages/auth/login';

import { Workbench } from './workbench';

import '@/assets/styles/index.css';

const router = createHashRouter([
  { path: '/auth/login', Component: Login },
  {
    path: '/',
    element: (
      <ProtectedRoute>
        <Workbench />
      </ProtectedRoute>
    )
  }
]);
await i18nReady;
await i18n.changeLanguage('en');
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <StyleProvider layer>
      <AppTheme>
        <RouterProvider router={router} />
      </AppTheme>
    </StyleProvider>
  </StrictMode>
);
