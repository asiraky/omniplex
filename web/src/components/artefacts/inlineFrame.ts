// Sizing for a page drawn in the transcript. The frame never scrolls inside
// itself, so it would not fight the transcript's scroll on touch: it takes the
// height the page reports, within a floor and a cap, and anything past the cap
// is cut off for the full viewer to show.

/** The least a frame is ever given. */
const MIN_PX = 120;
/** What a frame has before the page reports, and keeps for a page sized to
    its frame (a chart at 100vh), which has no height of its own to report. */
const UNREPORTED_PX = 360;
/** On a desktop the cap is fixed: a tall window is not a reason to give one
    reply the whole of it. */
const DESKTOP_CAP_PX = 640;
/** On a phone, the share of the screen one reply may take. */
const PHONE_CAP = 0.6;

/**
 * The frame's height for the height a page reports. `cut` says the page is
 * taller than the frame may be, so its bottom is hidden.
 */
export function inlineFrameHeight(
  reported: number | undefined,
  { desktop, viewportHeight }: { desktop: boolean; viewportHeight: number },
): { height: number; cut: boolean } {
  const cap = Math.max(MIN_PX, desktop ? DESKTOP_CAP_PX : Math.round(viewportHeight * PHONE_CAP));
  if (reported === undefined || !Number.isFinite(reported)) return { height: Math.min(cap, UNREPORTED_PX), cut: false };
  const want = Math.ceil(reported);
  return { height: Math.min(cap, Math.max(MIN_PX, want)), cut: want > cap };
}
