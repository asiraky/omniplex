/** The page's width and the scale that fits it into the stage: a device wider
    than the panel is shrunk whole rather than cropped, so a phone can still
    see what a desktop layout looks like. */
export function frameFit(
  deviceWidth: number | undefined,
  stageWidth: number,
): { width?: number; scale: number } {
  if (!deviceWidth || stageWidth <= 0) return { scale: 1 };
  return { width: deviceWidth, scale: Math.min(1, stageWidth / deviceWidth) };
}
