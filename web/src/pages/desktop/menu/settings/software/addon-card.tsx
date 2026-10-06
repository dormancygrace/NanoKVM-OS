import { type ReactNode } from 'react';

export const AddonCard = ({
  icon,
  title,
  children
}: {
  icon: ReactNode;
  title: string;
  children: ReactNode;
}) => (
  <div className="flex min-w-0 flex-col items-start gap-4 rounded-xl border border-neutral-700 bg-neutral-800/40 p-4">
    <div className="flex items-center gap-3 text-neutral-200">
      {icon}
      <div className="text-base font-medium">{title}</div>
    </div>
    {children}
  </div>
);
