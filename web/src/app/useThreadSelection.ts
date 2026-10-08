import { useCallback, useEffect, useRef, useState } from "react";

import { LAST_THREAD } from "./threadKeys";
import type { Wire } from "./useWire";

export type ThreadSelection = ReturnType<typeof useThreadSelection>;

/**
 * Which thread is attached, whether a new one is being written instead, and
 * whether the sidebar is covering them: the app's routing, such as it is.
 * Also where a reload goes back to the thread it was on, and where the app
 * lets go of a thread the list says is gone.
 */
export function useThreadSelection({
  wire,
  isDesktop,
  onLeave,
  onForget,
}: {
  wire: Wire;
  isDesktop: boolean;
  /** Called on every switch, to drop what belonged to the thread left. */
  onLeave: () => void;
  /** A hydrated thread that turned out to be gone before any list had it. */
  onForget: (id: string) => void;
}) {
  const { clientRef, activeRef, threads, threadsLoaded, setState, dropResume } = wire;
  const [activeId, setActiveId] = useState<string | null>(wire.resume?.state.threadId ?? null);
  useEffect(() => {
    activeRef.current = activeId;
  }, [activeRef, activeId]);
  // A thread being written but not yet sent, and the project it opened on.
  const [creating, setCreating] = useState<{ projectId?: string } | null>(null);

  // Whether the last-thread key was set at boot. Read once, before anything
  // can write it, because it decides what the very first frame shows. Storage
  // can be denied outright (Safari with cookies blocked), and a throw here
  // would take the whole mount with it.
  const [hadLastThread] = useState(() => {
    try {
      return localStorage.getItem(LAST_THREAD) !== null;
    } catch {
      return false;
    }
  });
  const restoreAttempted = useRef(false);
  // Open is the desktop default. On a phone the sidebar *is* the landing
  // screen: with nothing selected there is nothing behind it to look at, so
  // it starts open unless we are about to restore straight into a thread.
  const [sidebarOpen, setSidebarOpen] = useState(() => isDesktop || !hadLastThread);
  // Crossing the breakpoint resets it, but only on an actual crossing. On
  // mount this must leave the initial choice above alone.
  const [wasDesktop, setWasDesktop] = useState(isDesktop);
  if (wasDesktop !== isDesktop) {
    setWasDesktop(isDesktop);
    setSidebarOpen(isDesktop || activeId === null);
  }

  const select = useCallback(
    (id: string) => {
      // react-doctor-disable-next-line react-doctor/no-adjust-state-on-prop-change -- not derived state: selecting attaches the socket and writes storage, and the restore effect is the one caller that reacts to the thread list
      setCreating(null);
      setActiveId(id);
      activeRef.current = id;
      onLeave();
      setState(null);
      localStorage.setItem(LAST_THREAD, id);
      clientRef.current?.attach(id);
      // react-doctor-disable-next-line react-doctor/no-adjust-state-on-prop-change -- not derived state: selecting attaches the socket and writes storage, and the restore effect is the one caller that reacts to the thread list
      if (!isDesktop) setSidebarOpen(false);
    },
    [activeRef, clientRef, isDesktop, onLeave, setState],
  );

  // The draft takes the thread's place: nothing is attached while it is up,
  // and picking a thread from the list leaves it.
  const startNew = useCallback(() => {
    const projectId = threads.find((t) => t.id === activeRef.current)?.projectId;
    setCreating({ projectId });
    if (activeRef.current) {
      activeRef.current = null;
      setActiveId(null);
      setState(null);
      dropResume();
      clientRef.current?.detach();
    }
    if (!isDesktop) setSidebarOpen(false);
  }, [activeRef, clientRef, dropResume, isDesktop, setState, threads]);

  // The draft's own project pick, kept here so the Skills page reads the
  // project being drafted in and the draft comes back on it.
  const pickDraftProject = useCallback((projectId: string) => {
    setCreating((c) => c && { ...c, projectId });
  }, []);

  // Letting go of the attached thread because it is gone. On a phone that
  // leaves nothing behind the sidebar, so it comes back.
  const release = useCallback(() => {
    // react-doctor-disable-next-line react-doctor/no-adjust-state-on-prop-change -- not derived state: letting go detaches the socket, and only the thread list arriving can say the thread is gone
    setActiveId(null);
    setState(null);
    dropResume();
    clientRef.current?.detach();
    // react-doctor-disable-next-line react-doctor/no-adjust-state-on-prop-change -- not derived state: letting go detaches the socket, and only the thread list arriving can say the thread is gone
    if (!isDesktop) setSidebarOpen(true);
  }, [clientRef, dropResume, isDesktop, setState]);

  // Restore the last thread once the list arrives. This runs once: after it,
  // "no thread selected" is a state the user chose, not one we have yet to
  // resolve, and re-opening the sidebar under them would be wrong.
  useEffect(() => {
    if (!threadsLoaded || restoreAttempted.current) return;
    restoreAttempted.current = true;
    if (activeId) {
      // Hydrated from the resume cache before the list could say whether the
      // thread still exists. It usually does; when it doesn't (deleted or
      // closed from elsewhere while the page was dead) let go the same way
      // a live delete would. The seenActive effect below can't: it only acts
      // on threads it saw in a list first.
      if (threads.some((s) => s.id === activeId && s.phase !== "closed")) return;
      // Including the position the cache seeded: the scroll prune only drops
      // threads it saw in a list, and this one never made it into one.
      // react-doctor-disable-next-line react-doctor/no-pass-live-state-to-parent -- not live state: the scroll memory must drop a thread the list says is gone, which only this list arriving can tell
      onForget(activeId);
      release();
      return;
    }
    const last = localStorage.getItem(LAST_THREAD);
    const pick = threads.find((s) => s.id === last && s.phase !== "closed") ?? null;
    if (pick) select(pick.id);
    // Nothing to restore into, so the phone lands on the sidebar after all.
    // react-doctor-disable-next-line react-doctor/no-adjust-state-on-prop-change -- not derived state: the phone's sidebar reopens only once the first thread list shows nothing to restore
    else if (!isDesktop) setSidebarOpen(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [threadsLoaded, threads]);
  // Until the first list lands we do not know whether there is anything to
  // show, so the content column holds the space rather than announcing "all
  // caught up" to someone with six threads on a slow connection.
  const restoring = !threadsLoaded;

  // The attached thread went away (deleted elsewhere, or torn down here).
  //
  // "Absent from the list" only means gone if it was ever in the list: a
  // thread we just created is attached before the broadcast carrying it
  // arrives, and treating that gap as a disappearance would detach the
  // thread the user is watching being born. So it has to have been seen
  // first. Waiting for `state` instead would be the wrong test: deleting a
  // row that is not the open one selects it first, which clears state, so a
  // delete landing before the first snapshot would leave the app attached to
  // nothing and stuck on "Attaching…" forever.
  const seenActive = useRef<string | null>(null);
  useEffect(() => {
    if (!activeId) return;
    if (threads.some((s) => s.id === activeId)) {
      seenActive.current = activeId;
      return;
    }
    if (seenActive.current !== activeId) return;
    seenActive.current = null;
    release();
  }, [threads, activeId, release]);

  return {
    activeId,
    creating,
    restoring,
    sidebarOpen,
    setSidebarOpen,
    select,
    startNew,
    pickDraftProject,
  };
}
