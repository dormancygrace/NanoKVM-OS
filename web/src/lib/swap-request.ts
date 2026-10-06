// The size a swap request carries. Auto zram is 0; its sizeMiB is then the
// computed capacity (half of the Linux memory), not an offered size.
export function swapRequestSize(kind: 'zram' | 'sd', swap: { sizeMiB: number; auto?: boolean }) {
  return kind === 'zram' && swap.auto ? 0 : swap.sizeMiB;
}
