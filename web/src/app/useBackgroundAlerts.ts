import { useEffect, useRef } from "react";

import { playAlertSound, type AlertSound } from "~/lib/alertSound";
import { toast } from "~/lib/toast";
import type { ThreadMeta } from "~/protocol";
import { useLatest } from "~/useLatest";

import { alertFor, alertToast } from "./backgroundAlerts";

/** One live toast per thread: a newer alert for it replaces the last. */
const toastId = (threadId: string) => `thread-alert:${threadId}`;

/**
 * A toast when a thread you are not looking at finishes, fails, or needs you,
 * with a way straight to it. "Looking at" is the thread on screen with nothing
 * over it; behind a phone's open sidebar or a full-page screen, it counts as
 * elsewhere.
 *
 * It diffs each thread list the server sends against the last one it saw. The
 * first list only seeds that: whatever state things were in at load is not
 * news. The memory outlives a dropped connection, so the list a reconnect
 * brings is diffed against the one from before it, and a thread that finished
 * while the phone was offline still says so, once. A reload starts over and
 * says nothing; push notifications are for what happened while no page was
 * open.
 */
export function useBackgroundAlerts({
  threads,
  loaded,
  activeId,
  covered,
  open,
}: {
  threads: ThreadMeta[];
  loaded: boolean;
  activeId: string | null;
  covered: boolean;
  open: (id: string) => void;
}) {
  const seen = useRef<Map<string, ThreadMeta> | null>(null);
  // Threads with a toast that may still be up. One that timed out stays in
  // here; dismissing a toast that is already gone does nothing.
  const live = useRef(new Set<string>());
  // Read through refs so the diff runs once per list, not once per render:
  // App makes a fresh `open` every render, and a change of thread is the
  // effect below's business.
  const openRef = useLatest(open);
  const viewRef = useLatest({ activeId, covered });

  useEffect(() => {
    if (!loaded) return;
    const before = seen.current;
    seen.current = new Map(threads.map((t) => [t.id, t]));
    if (!before) return;

    const { activeId, covered } = viewRef.current;
    let sound: AlertSound | null = null;
    for (const next of threads) {
      const alert = alertFor(before.get(next.id), next);
      if (!alert || (next.id === activeId && !covered)) continue;
      const t = alertToast(alert, next);
      const id = next.id;
      toast[t.variant](t.title, {
        id: toastId(id),
        description: t.description,
        duration: t.duration,
        action: { label: "Open thread", onClick: () => openRef.current(id) },
      });
      live.current.add(id);
      // One chime for the whole list, however many alerts it carried. A
      // thread waiting on you outranks one that is only done.
      if (sound !== "needs") sound = t.sound;
    }
    if (sound) playAlertSound(sound);

    // A thread that has gone (deleted, or no longer listed) has nothing to
    // open, so its toast goes with it.
    for (const id of live.current) {
      if (seen.current.has(id)) continue;
      toast.dismiss(toastId(id));
      live.current.delete(id);
    }
  }, [threads, loaded, viewRef, openRef]);

  // Arriving at a thread, by the toast or any other way, answers its toast.
  useEffect(() => {
    if (!activeId || covered || !live.current.has(activeId)) return;
    toast.dismiss(toastId(activeId));
    live.current.delete(activeId);
  }, [activeId, covered]);
}
