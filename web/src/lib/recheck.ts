// The proof of the password (and second factor) that routes acting as root
// ask for: the server answers them with HTTP 403 and this code until the
// session has confirmed its credentials within a few minutes.
// Matches middleware.RecentAuthRequiredCode on the server.
export const recentAuthRequiredCode = -11;

export const RECHECK_EVENT = 'nano-kvm:recheck';

export type RecheckRequest = { resolve: (confirmed: boolean) => void };

let pending: Promise<boolean> | null = null;

// Asks the user to confirm their password (RecheckDialog, mounted once for
// the whole app). Resolves true when the server accepted it; requests that
// arrive meanwhile share the same dialog.
export function requestRecheck(): Promise<boolean> {
  if (pending) return pending;
  pending = new Promise<boolean>((resolve) => {
    window.dispatchEvent(new CustomEvent<RecheckRequest>(RECHECK_EVENT, { detail: { resolve } }));
  }).finally(() => {
    pending = null;
  });
  return pending;
}
