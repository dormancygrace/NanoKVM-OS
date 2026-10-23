import { Outlet } from 'react-router-dom';

import { NetworkTrialHost } from './network-trial.tsx';

export const Root = () => {
  return (
    <div className="h-screen w-screen">
      <Outlet />
      <NetworkTrialHost />
    </div>
  );
};
