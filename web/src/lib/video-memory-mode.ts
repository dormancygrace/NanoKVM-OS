// The video memory modes the server offers (see platform/build.sh): the
// resolution that a fixed carveout is sized for. The server maps the names of
// earlier releases (cma, fixed, fhd-fixed, qhd-fixed) to these.
export type VideoMemoryResolution = 'fhd' | 'qhd' | 'uhd';
export type VideoMemoryMode = VideoMemoryResolution;

export const videoMemoryResolutions: VideoMemoryResolution[] = ['fhd', 'qhd', 'uhd'];

export function parseVideoMemoryMode(mode: string): VideoMemoryResolution | undefined {
  return videoMemoryResolutions.find((resolution) => resolution === mode);
}

// The installed mode for a resolution, or nothing.
export function pickVideoMemoryMode(
  installed: readonly string[],
  resolution: VideoMemoryResolution
): VideoMemoryMode | undefined {
  return installed.includes(resolution) ? resolution : undefined;
}
