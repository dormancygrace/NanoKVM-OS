// The brand mark from the media kit: https://tailscale.com/press. It is one
// colour, white on dark and black on light, so it takes the colour of strong
// text of the theme.
const dots = [
  [30.5, 84.5],
  [84.5, 84.5],
  [84.5, 138.5],
  [138.5, 84.5]
];
const faint = [
  [30.5, 138.5],
  [138.5, 138.5],
  [30.5, 30.5],
  [84.5, 30.5],
  [138.5, 30.5]
];

export const Tailscale = () => (
  <svg
    className="text-fg-strong block shrink-0"
    width="18"
    height="18"
    viewBox="0 0 169 169"
    fill="currentColor"
    aria-hidden="true"
    focusable="false"
  >
    {dots.map(([x, y]) => (
      <circle key={`${x}-${y}`} cx={x} cy={y} r="18" />
    ))}
    {faint.map(([x, y]) => (
      <circle key={`${x}-${y}`} cx={x} cy={y} r="18" fillOpacity="0.4" />
    ))}
  </svg>
);
