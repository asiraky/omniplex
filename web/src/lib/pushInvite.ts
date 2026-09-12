// Asking a new device, once, whether it wants notifications.
//
// This cannot be the browser's own permission dialog raised on load, however
// much that is what "ask me when the app opens" sounds like. Safari on iOS —
// the device this matters most on — only honours Notification.requestPermission
// from a user gesture, and ignores it otherwise. Chrome does raise it, but
// prompting an unknown visitor on load is the pattern its quieter-permissions
// heuristics exist to punish, and a denial is close to permanent: the browser
// will not ask again, and the app cannot undo it.
//
// So the app asks first, in its own words, with a button. The button is the
// gesture, and it opens the real dialog. Someone who says "not now" is not
// asked again on this device; the Access panel is where they turn it on later.

const DISMISSED_KEY = "omniplex.push.invited";

/** Whether this device has already been asked and said no. */
function dismissed(): boolean {
  try {
    return localStorage.getItem(DISMISSED_KEY) === "1";
  } catch {
    // Private-mode storage can throw. Treat it as not yet asked: a second
    // invitation is a smaller problem than never offering notifications.
    return false;
  }
}

/** Remembers that this device was asked, so it is not asked on every load. */
export function markInvited(): void {
  try {
    localStorage.setItem(DISMISSED_KEY, "1");
  } catch {
    // Nothing to do. It will be asked again next time, which is survivable.
  }
}

/**
 * Whether to raise the invitation now.
 *
 * Takes the capability check as an argument rather than importing it, so this
 * stays a decision about *asking* and the browser questions live in one place.
 */
export function shouldInvite(canAsk: boolean): boolean {
  return canAsk && !dismissed();
}
