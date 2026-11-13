// Sizing for a page drawn in the transcript. The frame never scrolls inside
// itself, so it would not fight the transcript's scroll on touch: it takes the
// height the page reports, within a floor and a cap, and anything past the cap
// is cut off for the full viewer to show.

/** The least a frame is ever given, and what it has before the page reports. */
export const INLINE_MIN_PX = 120;
/** On a desktop the cap is fixed: a tall window is not a reason to give one
    reply the whole of it. */
export const INLINE_DESKTOP_CAP_PX = 640;
/** On a phone, the share of the screen one reply may take. */
export const INLINE_PHONE_CAP = 0.6;

/**
 * The frame's height for the height a page reports. `cut` says the page is
 * taller than the frame may be, so its bottom is hidden.
 */
export function inlineFrameHeight(
  reported: number | undefined,
  { desktop, viewportHeight }: { desktop: boolean; viewportHeight: number },
): { height: number; cut: boolean } {
  const cap = Math.max(INLINE_MIN_PX, desktop ? INLINE_DESKTOP_CAP_PX : Math.round(viewportHeight * INLINE_PHONE_CAP));
  const want = reported !== undefined && Number.isFinite(reported) ? Math.ceil(reported) : INLINE_MIN_PX;
  return { height: Math.min(cap, Math.max(INLINE_MIN_PX, want)), cut: want > cap };
}
