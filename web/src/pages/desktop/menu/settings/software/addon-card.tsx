import { type ReactNode } from 'react';

import { Panel } from '@/components/ui/settings.tsx';

export const AddonCard = ({
  icon,
  title,
  children
}: {
  icon: ReactNode;
  title: string;
  children: ReactNode;
}) => (
  <Panel className="flex min-w-0 flex-col items-start gap-4">
    <div className="text-fg flex items-center gap-3">
      {icon}
      <div className="text-base font-medium">{title}</div>
    </div>
    {children}
  </Panel>
);
