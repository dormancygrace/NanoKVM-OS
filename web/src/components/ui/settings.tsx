import { ComponentProps, ReactNode } from 'react';
import { Button, Divider, Tooltip } from 'antd';
import clsx from 'clsx';
import { CircleHelpIcon } from 'lucide-react';

// Building blocks for the settings pages. One page = SettingsPage; groups of
// related settings = SettingsSection; one setting = SettingRow (label and
// description on the left, the control on the right); bordered groups and
// lists = Panel. Colours come from the semantic tokens (lib/theme-tokens.ts).

type SettingsPageProps = {
  title: ReactNode;
  description?: ReactNode;
  // Page-level actions (for example a refresh button) next to the title.
  actions?: ReactNode;
  children: ReactNode;
};

export const SettingsPage = ({ title, description, actions, children }: SettingsPageProps) => (
  <div className="pb-6">
    <div className="flex items-center justify-between gap-3">
      <h2 className="text-fg m-0 text-base font-medium">{title}</h2>
      {actions && <div className="flex shrink-0 items-center gap-1">{actions}</div>}
    </div>
    {description && <p className="text-fg-muted mt-1 mb-0 text-sm">{description}</p>}
    <Divider className="my-4! opacity-50" />
    <div className="space-y-6">{children}</div>
  </div>
);

type SettingsSectionProps = {
  title?: ReactNode;
  description?: ReactNode;
  // Section-level actions (for example a refresh button) next to the title.
  actions?: ReactNode;
  children: ReactNode;
};

// Sections of a page are separated by space-y-6 (the page root), the rows
// inside a section by space-y-4.
export const SettingsSection = ({
  title,
  description,
  actions,
  children
}: SettingsSectionProps) => (
  <section className="space-y-3">
    {(title || description || actions) && (
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          {title && <h3 className="text-fg-muted m-0 text-sm font-medium">{title}</h3>}
          {description && <p className="text-fg-muted mt-1 mb-0 text-xs">{description}</p>}
        </div>
        {actions && <div className="flex shrink-0 items-center gap-1">{actions}</div>}
      </div>
    )}
    <div className="space-y-4">{children}</div>
  </section>
);

type SettingRowProps = {
  label: ReactNode;
  description?: ReactNode;
  // Extra explanation behind a help icon, for details most people do not need.
  help?: string;
  // id of the control. The description gets the id <htmlFor>-description;
  // pass it to the control as aria-describedby.
  htmlFor?: string;
  // Put the control under the label (wide controls: long selects, inputs).
  stacked?: boolean;
  children: ReactNode;
};

export const SettingRow = ({
  label,
  description,
  help,
  htmlFor,
  stacked,
  children
}: SettingRowProps) => {
  const descriptionId = htmlFor ? `${htmlFor}-description` : undefined;
  return (
    <div
      className={clsx(
        'flex gap-x-4 gap-y-2',
        stacked ? 'flex-col' : 'flex-wrap items-center justify-between sm:flex-nowrap'
      )}
    >
      <div className="min-w-0 space-y-0.5">
        <div className="text-fg flex items-center gap-1.5">
          {htmlFor ? <label htmlFor={htmlFor}>{label}</label> : <span>{label}</span>}
          {help && <HelpTip title={help} />}
        </div>
        {description && (
          <div id={descriptionId} className="text-fg-muted text-xs">
            {description}
          </div>
        )}
      </div>
      <div className={clsx('max-w-full', stacked ? 'w-full' : 'ml-auto shrink-0')}>{children}</div>
    </div>
  );
};

// A help icon for neutral extra information. The tooltip text is also the
// icon's accessible name, and the tooltip opens on keyboard focus too.
export const HelpTip = ({ title }: { title: string }) => (
  <Tooltip
    title={title}
    trigger={['hover', 'focus']}
    placement="right"
    styles={{ root: { maxWidth: 400 } }}
  >
    <button
      type="button"
      aria-label={title}
      className="nanokvm-button-base text-fg-muted inline-flex shrink-0 cursor-help items-center"
    >
      <CircleHelpIcon size={14} aria-hidden />
    </button>
  </Tooltip>
);

type PanelProps = {
  className?: string;
  // No padding, for lists whose rows bring their own (divide-y divide-line).
  flush?: boolean;
  children: ReactNode;
};

export const Panel = ({ className, flush, children }: PanelProps) => (
  <div className={clsx('border-line bg-surface rounded-lg border', !flush && 'p-4', className)}>
    {children}
  </div>
);

type Tone = 'success' | 'warning' | 'danger' | 'info' | 'neutral';

const toneClass: Record<Tone, string> = {
  success: 'text-success',
  warning: 'text-warning',
  danger: 'text-danger',
  info: 'text-info',
  neutral: 'text-fg-muted'
};

// A state shown as a coloured dot plus text, never colour alone.
export const StatusBadge = ({ tone, children }: { tone: Tone; children: ReactNode }) => (
  <span className={clsx('inline-flex items-center gap-1.5 text-xs', toneClass[tone])}>
    <span className="size-1.5 shrink-0 rounded-full bg-current" aria-hidden />
    <span>{children}</span>
  </span>
);

type IconButtonProps = Omit<
  ComponentProps<typeof Button>,
  'type' | 'size' | 'icon' | 'children'
> & {
  // Accessible name and tooltip.
  label: string;
  icon: ReactNode;
};

// The remaining props (including the ref and the event handlers a wrapping
// Popconfirm or Popover injects) go to the button.
export const IconButton = ({ label, icon, ...props }: IconButtonProps) => (
  <Tooltip title={label}>
    <Button type="text" size="small" aria-label={label} icon={icon} {...props} />
  </Tooltip>
);
