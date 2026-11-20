import { IconBase, type IconProps } from './icon-base.tsx';

// Three keys in a frame: Ctrl and Alt below, Del above.
export const CtrlAltDel = (props: IconProps) => {
  return (
    <IconBase {...props}>
      <path stroke="none" d="M0 0h24v24H0z" fill="none" />
      <rect x="3" y="3" width="18" height="18" rx="3" />
      <g fill="currentColor" stroke="none">
        <rect x="12.75" y="7.25" width="4" height="4" rx="0.75" />
        <rect x="7.25" y="12.75" width="4" height="4" rx="0.75" />
        <rect x="12.75" y="12.75" width="4" height="4" rx="0.75" />
      </g>
    </IconBase>
  );
};
