export const WifiSignal = ({ signal }: { signal: number }) => {
  const level = signal >= -55 ? 3 : signal >= -67 ? 2 : signal >= -78 ? 1 : 0;
  const label = `Wi-Fi: ${Math.round(signal)} dBm`;
  const color = (minimum: number) => (level >= minimum ? 'text-neutral-200' : 'text-neutral-700');

  return (
    <svg
      viewBox="0 0 24 24"
      width="26"
      height="26"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      role="img"
      aria-label={label}
      className="shrink-0"
    >
      <title>{label}</title>
      <path d="M2 8.5a15 15 0 0 1 20 0" className={color(3)} />
      <path d="M5.5 12a10 10 0 0 1 13 0" className={color(2)} />
      <path d="M9 15.5a5 5 0 0 1 6 0" className={color(1)} />
      <circle cx="12" cy="19" r="1" fill="currentColor" className="text-neutral-200" />
    </svg>
  );
};
