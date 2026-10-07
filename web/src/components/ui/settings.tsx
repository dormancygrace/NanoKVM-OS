import { ReactNode } from 'react';
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
  children: ReactNode;
};

export const SettingsSection = ({ title, description, children }: SettingsSectionProps) => (
  <section className="space-y-3">
    {(title || description) && (
      <div>
        {title && <h3 className="text-fg-muted m-0 text-sm font-medium">{title}</h3>}
        {description && <p className="text-fg-muted mt-1 mb-0 text-xs">{description}</p>}
      </div>
    )}
    {children}
  </section>
);

type SettingRowProps = {
  label: ReactNode;
  description?: ReactNode;
  // Extra explanation behind a help icon, for details most people do not need.
  help?: ReactNode;
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
          {help && (
            <Tooltip title={help}>
              <CircleHelpIcon size={14} className="text-fg-muted shrink-0" aria-hidden />
            </Tooltip>
          )}
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

type PanelProps = {
  className?: string;
  children: ReactNode;
};

export const Panel = ({ className, children }: PanelProps) => (
  <div className={clsx('border-line bg-surface rounded-lg border p-4', className)}>{children}</div>
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

type IconButtonProps = {
  // Accessible name and tooltip.
  label: string;
  icon: ReactNode;
  onClick?: () => void;
  danger?: boolean;
  loading?: boolean;
  disabled?: boolean;
};

export const IconButton = ({
  label,
  icon,
  onClick,
  danger,
  loading,
  disabled
}: IconButtonProps) => (
  <Tooltip title={label}>
    <Button
      type="text"
      size="small"
      aria-label={label}
      icon={icon}
      danger={danger}
      loading={loading}
      disabled={disabled}
      onClick={onClick}
    />
  </Tooltip>
);
