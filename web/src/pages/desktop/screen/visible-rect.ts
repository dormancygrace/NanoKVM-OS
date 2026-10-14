type FrameGeometry = {
  codedWidth?: number;
  codedHeight?: number;
  visibleRect?: { x: number; y: number; width: number; height: number } | null;
};

// Edge's hardware HEVC decoder reports the chroma plane's size as the visible
// rectangle of an NV12 frame: exactly half the coded size in each direction,
// at the origin (2560x1440 coded, 1280x720 visible). Drawing such a frame
// shows its top-left quarter enlarged. halvedVisibleRect returns the doubled
// rectangle for that case and null for any other frame. A real crop removes
// less than one 16-pixel macroblock of padding, so a doubled rectangle that
// is more than that short of the coded size is not this bug.
export function halvedVisibleRect(frame: FrameGeometry) {
  const rect = frame.visibleRect;
  const { codedWidth, codedHeight } = frame;
  if (!rect || !codedWidth || !codedHeight || rect.x !== 0 || rect.y !== 0) return null;
  const width = rect.width * 2;
  const height = rect.height * 2;
  if (width > codedWidth || height > codedHeight) return null;
  if (codedWidth - width >= 16 || codedHeight - height >= 16) return null;
  return { x: 0, y: 0, width, height };
}
