// Surviving a tab discard without the reader noticing.
//
// A mobile browser quietly kills a backgrounded tab after a while and reloads
// the page when it returns. Without help that return is a cold boot: a blank
// column, "Attaching…", a snapshot round-trip, and a jump back to roughly
// where the reader was. So the page saves its attached thread's state — and
// where it was scrolled — as it goes to background, and a fresh boot hydrates
// from that cache: the first frame is the transcript as they left it, and the
// socket then fetches only what happened while the page was dead (attach with
// afterSeq; see Client.prime).
//
// sessionStorage on purpose: it is per-tab, it survives exactly the
// discard-and-reload this exists for, and it dies with the tab instead of
// accumulating transcripts in localStorage forever.

import { currentBuild } from "./boot";
import type { ThreadState } from "./protocol";

const KEY = "omniplex.resume";

export interface ResumeSnapshot {
  /** The bundle that wrote this. A different bundle may disagree about the
      shape of ThreadState, so its cache is not trusted. */
  build: string;
  state: ThreadState;
  scrollTop: number;
  /** Pinned to the tail when saved, so the restore re-arms the follow pin
      rather than parking the view at a stale offset near the bottom. */
  atBottom: boolean;
}

export function saveResume(state: ThreadState, scrollTop: number, atBottom: boolean) {
  try {
    sessionStorage.setItem(
      KEY,
      JSON.stringify({ build: currentBuild(), state, scrollTop, atBottom } satisfies ResumeSnapshot),
    );
  } catch {
    // Quota, or storage blocked outright. A cold attach is the fallback, not
    // an error — but a stale blob must not outlive a failed refresh of it.
    try {
      sessionStorage.removeItem(KEY);
    } catch {
      // Nothing left to do.
    }
  }
}

/**
 * The cached snapshot, if it is trustworthy: same bundle, and the thread the
 * app would restore anyway (the cache accelerates the restore; it never gets
 * to pick a different thread than localStorage's last-thread key would).
 */
export function loadResume(lastThreadId: string | null): ResumeSnapshot | null {
  try {
    const raw = sessionStorage.getItem(KEY);
    if (!raw || !lastThreadId) return null;
    const snap = JSON.parse(raw) as ResumeSnapshot;
    if (snap.build !== currentBuild()) return null;
    if (!snap.state || snap.state.threadId !== lastThreadId) return null;
    if (typeof snap.state.seq !== "number") return null;
    return snap;
  } catch {
    return null;
  }
}
