import { Tag } from 'antd';

// One colour per band, so 2.4 and 5 GHz networks tell apart at a glance.
const colors: Record<string, string> = { '2.4': 'gold', '5': 'geekblue', '6': 'purple' };

export const WifiBandTag = ({ band }: { band: string }) => (
  <Tag color={colors[band]} className="m-0 text-xs">
    {band} GHz
  </Tag>
);
