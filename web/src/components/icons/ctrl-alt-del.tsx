import { IconBase, type IconProps } from './icon-base.tsx';

// Three keys: Ctrl and Alt below, Del above and a little to the right.
export const CtrlAltDel = (props: IconProps) => {
  return (
    <IconBase {...props}>
      <path stroke="none" d="M0 0h24v24H0z" fill="none" />
      <g fill="currentColor" stroke="none">
        <rect x="3.5" y="13" width="6.5" height="6.5" rx="1.4" />
        <rect x="11.5" y="13" width="6.5" height="6.5" rx="1.4" />
        <rect x="14.5" y="4.5" width="6.5" height="6.5" rx="1.4" />
      </g>
    </IconBase>
  );
};
