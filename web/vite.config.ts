import { readFileSync } from 'node:fs';
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig, type Plugin } from 'vite';

// MSW's service worker is only needed with `--mode mocked`. It lives outside
// public/ so that production builds do not ship it; this plugin serves it
// (dev server) or emits it (build) in mocked mode only.
const mswWorker = 'mockServiceWorker.js';
const mswWorkerUrl = new URL(`./msw/${mswWorker}`, import.meta.url);

function mockServiceWorker(): Plugin {
  return {
    name: 'nanokvm-mock-service-worker',
    configureServer(server) {
      server.middlewares.use(`/${mswWorker}`, (_req, res) => {
        res.setHeader('Content-Type', 'text/javascript');
        res.end(readFileSync(mswWorkerUrl));
      });
    },
    generateBundle() {
      this.emitFile({ type: 'asset', fileName: mswWorker, source: readFileSync(mswWorkerUrl) });
    }
  };
}

// The device serves every asset itself over TLS on one slow core, so each
// request is costly. Automatic splitting produced about 200 chunks, most of
// them a few hundred bytes (single icons, antd helpers, API wrappers) for
// every distinct set of lazy importers; the desktop page needed 130 requests.
// These groups merge them:
// - vendor: everything the entry loads statically, as one file;
// - icons, antd-icons: all icons, which the desktop page mostly needs anyway;
// - antd: still split by the set of lazy chunks that use it, but with small
//   sets merged into a neighbour;
// - app: the API wrappers and atoms, which are small and widely shared.
// Merging small sets can create import cycles between chunks (it did for app
// code split the same way); tests/chunks.test.mjs checks that there are none.
// Large optional parts (terminal, picoclaw, virtual keyboard, settings pages,
// players, locales) stay lazily loaded chunks of their own.
const htmlOrMain = /\.html$|[\\/]src[\\/]main\.tsx$/;
const codeSplitting = {
  groups: [
    {
      name: 'vendor',
      tags: ['$initial' as const],
      test: (id: string) => !htmlOrMain.test(id),
      priority: 10
    },
    { name: 'icons', test: /[\\/]node_modules[\\/].*lucide-react[\\/]/, priority: 5 },
    {
      name: 'antd-icons',
      test: /[\\/]node_modules[\\/].*@ant-design[\\/]icons(-svg)?[\\/]/,
      priority: 5
    },
    {
      name: 'antd',
      test: /[\\/]node_modules[\\/].*(antd|@rc-component|rc-[a-z-]+|@ant-design)[\\/]/,
      entriesAware: true,
      entriesAwareMergeThreshold: 96 * 1024,
      priority: 4
    },
    {
      name: 'app',
      test: /[\\/]src[\\/](api|jotai)[\\/]|[\\/]node_modules[\\/].*jotai[\\/]/,
      priority: 3
    }
  ]
};

export default defineConfig(({ mode }) => ({
  plugins: [react(), tailwindcss(), ...(mode === 'mocked' ? [mockServiceWorker()] : [])],
  resolve: {
    tsconfigPaths: true
  },
  server: {
    port: 3001
  },
  build: {
    // The vendor chunk holds all code the entry needs before the first render
    // (React, antd core, router, i18n): about 740 kB minified, 240 kB gzipped.
    chunkSizeWarningLimit: 800,
    rolldownOptions: {
      output: {
        codeSplitting,
        // entriesAware chunks are named after every importer ("antd~a~b~…");
        // keep only the group name.
        chunkFileNames: (chunk) => `assets/${chunk.name.split('~')[0]}-[hash].js`
      }
    }
  }
}));
