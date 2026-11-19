import { createHashRouter, Outlet } from 'react-router-dom';

import { AdminRoute, ProtectedRoute } from '@/components/auth';
import { LazyRoot } from '@/components/lazy-root';

export const router = createHashRouter([
  {
    path: '/auth/login',
    lazy: async () => {
      const { Login } = await import('./pages/auth/login');
      return { Component: Login };
    }
  },
  {
    path: '/',
    element: (
      <ProtectedRoute>
        <LazyRoot />
      </ProtectedRoute>
    ),
    children: [
      {
        // the overview or the remote screen, as the device is set
        path: '',
        lazy: async () => {
          const { Home } = await import('./pages/home');
          return { Component: Home };
        }
      },
      {
        path: 'screen',
        lazy: async () => {
          const { Desktop } = await import('./pages/desktop');
          return { Component: Desktop };
        }
      },
      {
        path: 'overview',
        lazy: async () => {
          const { Overview } = await import('./pages/overview');
          return { Component: Overview };
        }
      },
      {
        element: (
          <AdminRoute>
            <Outlet />
          </AdminRoute>
        ),
        children: [
          {
            path: 'terminal',
            lazy: async () => {
              const { Terminal } = await import('./pages/terminal');
              return { Component: Terminal };
            }
          }
        ]
      },
      {
        path: 'auth/password',
        lazy: async () => {
          const { Password } = await import('./pages/auth/password');
          return { Component: Password };
        }
      }
    ]
  },
  {
    // Without a session: the device was just found at this address.
    path: '/network/confirm',
    lazy: async () => {
      const { NetworkConfirm } = await import('./pages/network-confirm');
      return { Component: NetworkConfirm };
    }
  },
  {
    path: '/wifi',
    caseSensitive: false,
    lazy: async () => {
      const { Wifi } = await import('./pages/wifi');
      return { Component: Wifi };
    }
  }
]);
