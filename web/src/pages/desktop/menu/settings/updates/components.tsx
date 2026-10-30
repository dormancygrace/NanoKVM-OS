import { type ReactNode } from 'react';
import { Tag } from 'antd';
import { ChevronDownIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import type { Components } from '@/api/updates.ts';
import { componentLabel } from '@/lib/update-components.ts';
import { Panel } from '@/components/ui/settings.tsx';

// A folded part of the page, closed until opened.
export const Fold = ({ title, children }: { title: string; children: ReactNode }) => (
  <Panel flush className="overflow-hidden">
    <details className="group">
      <summary className="text-fg-muted hover:text-fg flex cursor-pointer list-none items-center justify-between gap-3 px-4 py-3 text-sm [&::-webkit-details-marker]:hidden">
        <span>{title}</span>
        <ChevronDownIcon size={16} className="transition-transform group-open:rotate-180" />
      </summary>
      <div className="space-y-3 px-4 pb-4">{children}</div>
    </details>
  </Panel>
);

// The installed NanoKVM OS packages, with the newer version the last check
// found, and the Alpine and kernel releases.
export const ComponentList = ({ components }: { components?: Components }) => {
  const { t } = useTranslation();
  const tr = (key: string, options?: Record<string, string>) =>
    t(`settings.updates.components.${key}`, options);
  const kernelChanges =
    !!components?.bootKernel && !!components.kernel && components.bootKernel !== components.kernel;
  return (
    <>
      <p className="text-fg-muted mt-0 text-sm">{tr('description')}</p>
      <table className="w-full border-collapse text-sm">
        <tbody>
          {components?.packages.map((component) => (
            <tr key={component.name} className="border-t border-neutral-700/60 first:border-t-0">
              <td className="py-1.5 pr-3">
                <div>{componentLabel(t, component.name)}</div>
                <div className="text-fg-muted text-xs">{component.name}</div>
              </td>
              <td className="py-1.5 text-right whitespace-nowrap tabular-nums">
                {component.installed}
                {component.available && (
                  <Tag color="blue" className="ms-2 me-0">
                    {tr('available', { version: component.available })}
                  </Tag>
                )}
              </td>
            </tr>
          ))}
          <tr className="border-t border-neutral-700/60">
            <td className="py-1.5 pr-3">{tr('alpine')}</td>
            <td className="py-1.5 text-right tabular-nums">{components?.alpine || '—'}</td>
          </tr>
          <tr className="border-t border-neutral-700/60">
            <td className="py-1.5 pr-3">{tr('kernel')}</td>
            <td className="py-1.5 text-right tabular-nums">
              {components?.kernel || '—'}
              {kernelChanges && (
                <div className="text-fg-muted text-xs">
                  {tr('kernelNext', { version: components.bootKernel })}
                </div>
              )}
            </td>
          </tr>
        </tbody>
      </table>
    </>
  );
};
