type ResponsiveDeviceInput = {
  width: number;
  height: number;
  hasTouch: boolean;
  coarsePointer: boolean;
  userAgent: string;
};

export type ResponsiveDeviceState = {
  isTouchLikeDevice: boolean;
  isPortraitViewport: boolean;
  isLandscapeViewport: boolean;
  isMobilePortrait: boolean;
};

export function getResponsiveDeviceState(input: ResponsiveDeviceInput): ResponsiveDeviceState {
  const isMobileUserAgent = /Android|iPhone|iPad|iPod|Mobile|Tablet/i.test(input.userAgent);
  const isTouchLikeDevice = input.hasTouch || input.coarsePointer || isMobileUserAgent;
  const isPortraitViewport = input.height > input.width;
  const isLandscapeViewport = input.width >= input.height;

  return {
    isTouchLikeDevice,
    isPortraitViewport,
    isLandscapeViewport,
    isMobilePortrait: isTouchLikeDevice && isPortraitViewport
  };
}
