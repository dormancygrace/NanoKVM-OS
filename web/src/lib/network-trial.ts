import type { NetworkTrial } from '@/api/network.ts';

// A network change on trial, kept for this tab so that the check goes on
// after a reload or with the settings closed.
export type PendingTrial = { trial: NetworkTrial; deadline: number };
// ended: the device no longer waits for this change; it was kept (also from
// another tab), or undone early because the interface did not restart.
export type NetworkTrialResult = 'kept' | 'reverted' | 'ended';

const storageKey = 'nanokvm.networkTrial';
export const networkTrialStarted = 'nanokvm:network-trial';
export const networkTrialDone = 'nanokvm:network-trial-done';

export function startNetworkTrial(trial: NetworkTrial) {
  const pending: PendingTrial = { trial, deadline: Date.now() + trial.expiresIn * 1000 };
  try {
    sessionStorage.setItem(storageKey, JSON.stringify(pending));
  } catch {
    // Without storage the check still runs until this page is left.
  }
  window.dispatchEvent(new CustomEvent<PendingTrial>(networkTrialStarted, { detail: pending }));
}

export function pendingNetworkTrial(): PendingTrial | null {
  try {
    const pending = JSON.parse(sessionStorage.getItem(storageKey) ?? 'null') as PendingTrial | null;
    if (pending?.trial?.token && pending.deadline > Date.now()) return pending;
    sessionStorage.removeItem(storageKey);
  } catch {
    // Nothing to resume.
  }
  return null;
}

// Forgets the trial once its outcome is known, so a reload does not resume it.
export function forgetNetworkTrial() {
  try {
    sessionStorage.removeItem(storageKey);
  } catch {
    // Already gone.
  }
}

export function finishNetworkTrial(result: NetworkTrialResult) {
  forgetNetworkTrial();
  window.dispatchEvent(new CustomEvent<NetworkTrialResult>(networkTrialDone, { detail: result }));
}

// The device's page at another address, with this page's scheme and port.
export function originOf(host: string) {
  const name = host.includes(':') ? `[${host}]` : host;
  const port = window.location.port ? `:${window.location.port}` : '';
  return `${window.location.protocol}//${name}${port}`;
}

export function networkConfirmURL(host: string, token: string) {
  return `${originOf(host)}/#/network/confirm?token=${encodeURIComponent(token)}`;
}

// What the device at host says about the trial: applied (reachable with the
// change in effect), pending (reachable, still running its previous
// settings), ended (reachable, nothing waits any more), or null when it does
// not answer.
export async function probeTrial(host: string, id: string, timeout = 1500) {
  try {
    const response = await fetch(`${originOf(host)}/api/network/trial/probe`, {
      cache: 'no-store',
      signal: AbortSignal.timeout(timeout)
    });
    if (!response.ok) return null;
    const body = await response.json();
    if (body?.data?.id === '') return 'ended';
    if (body?.data?.id !== id) return null;
    return body?.data?.applied === true ? 'applied' : 'pending';
  } catch {
    return null;
  }
}
