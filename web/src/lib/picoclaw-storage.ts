const PICOCLAW_SESSION_ID_KEY = 'nano-kvm-picoclaw-session-id';
const PICOCLAW_MAX_STEPS_KEY = 'nano-kvm-picoclaw-max-steps';
const PICOCLAW_MAX_RUNTIME_KEY = 'nano-kvm-picoclaw-max-runtime-ms';
const PICOCLAW_RUNTIME_INSTALL_SNAPSHOT_KEY = 'nano-kvm-picoclaw-runtime-install-snapshot';

export type PicoclawRuntimeInstallSnapshot = {
  installing: boolean;
  installProgress?: number;
  installStage?: string;
  status?: string;
};

function getSessionStore() {
  try {
    return window.sessionStorage;
  } catch {
    return null;
  }
}

export function setPicoclawSessionId(sessionId: string) {
  const sessionStore = getSessionStore();
  sessionStore?.setItem(PICOCLAW_SESSION_ID_KEY, sessionId);
  localStorage.setItem(PICOCLAW_SESSION_ID_KEY, sessionId);
}

export function clearPicoclawSessionId() {
  const sessionStore = getSessionStore();
  sessionStore?.removeItem(PICOCLAW_SESSION_ID_KEY);
  localStorage.removeItem(PICOCLAW_SESSION_ID_KEY);
}

export function getPicoclawMaxSteps() {
  const value = localStorage.getItem(PICOCLAW_MAX_STEPS_KEY);
  const parsed = value ? Number(value) : 20;
  return Math.max(1, Math.min(50, Number.isFinite(parsed) ? parsed : 20));
}

export function getPicoclawMaxRuntimeMs() {
  const value = localStorage.getItem(PICOCLAW_MAX_RUNTIME_KEY);
  const parsed = value ? Number(value) : 120000;
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return 120000;
  }
  return parsed;
}

export function getPicoclawRuntimeInstallSnapshot(): PicoclawRuntimeInstallSnapshot | null {
  const value = localStorage.getItem(PICOCLAW_RUNTIME_INSTALL_SNAPSHOT_KEY);
  if (!value) {
    return null;
  }

  try {
    const parsed = JSON.parse(value) as PicoclawRuntimeInstallSnapshot;
    if (!parsed || typeof parsed !== 'object') {
      return null;
    }
    return {
      installing: parsed.installing === true,
      installProgress:
        typeof parsed.installProgress === 'number' ? parsed.installProgress : undefined,
      installStage: typeof parsed.installStage === 'string' ? parsed.installStage : undefined,
      status: typeof parsed.status === 'string' ? parsed.status : undefined
    };
  } catch {
    return null;
  }
}

export function setPicoclawRuntimeInstallSnapshot(snapshot: PicoclawRuntimeInstallSnapshot) {
  localStorage.setItem(PICOCLAW_RUNTIME_INSTALL_SNAPSHOT_KEY, JSON.stringify(snapshot));
}

export function clearPicoclawRuntimeInstallSnapshot() {
  localStorage.removeItem(PICOCLAW_RUNTIME_INSTALL_SNAPSHOT_KEY);
}
