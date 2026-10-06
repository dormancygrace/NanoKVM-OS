import icon from '@/assets/images/rustdesk.svg';

export const RustDeskIcon = ({ size = 18 }: { size?: number }) => (
  <img src={icon} className="block shrink-0" width={size} height={size} alt="" aria-hidden="true" />
);
