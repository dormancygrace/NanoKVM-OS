// The video memory modes the server offers (see platform/build.sh): a
// resolution the memory is sized for and whether it is a fixed carveout, which
// Linux never uses, instead of reusable CMA. There is no UHD in CMA, so UHD is
// always fixed and its mode name has no suffix.
export type VideoMemoryResolution = 'fhd' | 'qhd' | 'uhd';
export type VideoMemoryMode = 'fhd' | 'fhd-fixed' | 'qhd' | 'qhd-fixed' | 'uhd';

export const videoMemoryResolutions: VideoMemoryResolution[] = ['fhd', 'qhd', 'uhd'];

export function parseVideoMemoryMode(
  mode: string
): { resolution: VideoMemoryResolution; fixed: boolean } | undefined {
  switch (mode) {
    case 'fhd':
    case 'qhd':
      return { resolution: mode, fixed: false };
    case 'fhd-fixed':
      return { resolution: 'fhd', fixed: true };
    case 'qhd-fixed':
      return { resolution: 'qhd', fixed: true };
    case 'uhd':
      return { resolution: 'uhd', fixed: true };
    default:
      return undefined;
  }
}

export function isFixedOnly(resolution: VideoMemoryResolution) {
  return resolution === 'uhd';
}

export function composeVideoMemoryMode(
  resolution: VideoMemoryResolution,
  fixed: boolean
): VideoMemoryMode {
  if (isFixedOnly(resolution)) return 'uhd';
  return fixed ? `${resolution}-fixed` : resolution;
}

// The installed mode closest to a wish: the composed mode, else the other
// variant of the same resolution, else nothing.
export function pickVideoMemoryMode(
  installed: readonly string[],
  resolution: VideoMemoryResolution,
  fixed: boolean
): VideoMemoryMode | undefined {
  const wanted = composeVideoMemoryMode(resolution, fixed);
  if (installed.includes(wanted)) return wanted;
  const other = composeVideoMemoryMode(resolution, !fixed);
  return installed.includes(other) ? other : undefined;
}

// Whether the Fixed checkbox can change anything for this resolution.
export function canChooseFixed(installed: readonly string[], resolution: VideoMemoryResolution) {
  return (
    !isFixedOnly(resolution) &&
    installed.includes(composeVideoMemoryMode(resolution, false)) &&
    installed.includes(composeVideoMemoryMode(resolution, true))
  );
}
