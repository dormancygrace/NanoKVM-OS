export type System = 'Windows' | 'macOS' | 'Linux' | 'Unknown';

export function getOperatingSystem(): System {
  if (typeof window === 'undefined') {
    return 'Unknown';
  }

  if ('userAgentData' in navigator) {
    // @ts-expect-error check userAgentData.platform
    const platform = navigator.userAgentData?.platform?.toLowerCase();
    if (platform) {
      if (platform === 'windows') return 'Windows';
      if (platform === 'macos') return 'macOS';
      if (platform === 'linux' || platform === 'android') return 'Linux';
    }
  }

  // Fallback to User Agent
  const userAgent = navigator.userAgent;

  if (/Win/i.test(userAgent)) return 'Windows';
  if (/Mac|iPhone|iPod|iPad/i.test(userAgent)) return 'macOS';
  if (/Linux|Android/i.test(userAgent)) return 'Linux';

  return 'Unknown';
}
