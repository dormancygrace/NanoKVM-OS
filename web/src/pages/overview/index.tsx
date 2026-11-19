import clsx from 'clsx';
import { useAtomValue, useSetAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { captureReadyAtom, isHdmiEnabledAtom } from '@/jotai/screen.ts';
import { settingsRequestAtom } from '@/jotai/settings.ts';
import { Head } from '@/components/head.tsx';
import { Dashboard } from '@/pages/desktop/menu/settings/dashboard';

import { ScreenPreview } from './screen-preview.tsx';
import { OverviewShell } from './shell.tsx';

// The start page of a device that is managed more than it is watched: its
// state, with the remote screen one click away. It takes no part in the
// video: no stream, no input.
export const Overview = () => {
  const { t } = useTranslation();
  // the shell opens the requested settings page
  const openSettings = useSetAtom(settingsRequestAtom);
  // With the capture switched off there is no screen to show: its panel is
  // left out, like the other panels of what is not in use.
  const captureReady = useAtomValue(captureReadyAtom);
  const captureEnabled = useAtomValue(isHdmiEnabledAtom);
  const captureOff = captureReady && !captureEnabled;

  return (
    <OverviewShell>
      <Head title={t('overview.title')} />
      <div className="h-full overflow-y-auto">
        <main
          className={clsx(
            'mx-auto grid max-w-[1280px] gap-4 p-4',
            !captureOff && 'lg:grid-cols-[minmax(0,1fr)_360px]'
          )}
        >
          <div className="min-w-0">
            <Dashboard navigate={openSettings} caption={false} />
          </div>
          {!captureOff && (
            <aside className="order-first lg:order-none">
              <ScreenPreview />
            </aside>
          )}
        </main>
      </div>
    </OverviewShell>
  );
};
