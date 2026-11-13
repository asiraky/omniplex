import { useCallback, useEffect, useRef, useState } from "react";

import type { WorkspaceKind } from "~/components/threadDraft/shared";
import { isDraftEntry, navigate } from "~/lib/history";
import { openThreadMessage, threadIdFromPath, threadPath } from "~/lib/threadLinks";
import type { ThreadMeta } from "~/protocol";
import { useLatest } from "~/useLatest";

import { LAST_THREAD } from "./threadKeys";
import type { Wire } from "./useWire";

export type ThreadSelection = ReturnType<typeof useThreadSelection>;

/** Where a new thread should open, when the request says (as "New thread in…" does). */
export interface DraftPreset {
  projectId?: string;
  /** "" is the whole project. */
  folderId?: string;
  kind?: WorkspaceKind;
  /** With kind "attach": the existing worktree's path. */
  attachPath?: string;
  /** Put the cursor in the draft's composer as it opens. */
  focus?: boolean;
}

/**
 * How a pick writes the browser's history. A pick by the user pushes, so Back
 * returns to where they were; one that only puts right what the page already
 * shows replaces; one made by Back or Forward writes nothing, since the
 * browser has already moved.
 */
export type HistoryWrite = "push" | "replace" | "none";

/**
 * Which thread is attached, whether a new one is being written instead, and
 * whether the sidebar is covering them: the app's routing. The URL follows
 * the pick, /thread/<id> for a thread and / for a draft or nothing, one
 * history entry per place, so Back and Forward (Android's back gesture too)
 * move between threads. Also where a reload goes back to the thread it was
 * on, and where the app lets go of a thread the list says is gone.
 */
export function useThreadSelection({
  wire,
  isDesktop,
  onLeave,
  onForget,
  onOpenLink,
}: {
  wire: Wire;
  isDesktop: boolean;
  /** Called on every switch, to drop what belonged to the thread left. */
  onLeave: () => void;
  /** A hydrated thread that turned out to be gone before any list had it. */
  onForget: (id: string) => void;
  /** A tapped notification is opening a thread: clear whatever covers the thread view. */
  onOpenLink: () => void;
}) {
  const { clientRef, activeRef, threads, threadsLoaded, threadStore, dropResume } = wire;
  const [activeId, setActiveId] = useState<string | null>(wire.resume?.state.threadId ?? null);
  useEffect(() => {
    activeRef.current = activeId;
  }, [activeRef, activeId]);
  // A thread being written but not yet sent, and where it opened. The nonce
  // is new per request, so the app can key the draft on it and a second
  // request while one is open starts over on its own values.
  const [creating, setCreating] = useState<(DraftPreset & { nonce: number }) | null>(null);
  const nonce = useRef(0);

  // Whether the last-thread key was set at boot. Read once, before anything
  // can write it, because it decides what the very first frame shows. Storage
  // can be denied outright (Safari with cookies blocked), and a throw here
  // would take the whole mount with it.
  // A /thread/<id> link counts the same: it is about to open a thread.
  const [hadLastThread] = useState(() => {
    if (wire.link) return true;
    try {
      return localStorage.getItem(LAST_THREAD) !== null;
    } catch {
      return false;
    }
  });
  const restoreAttempted = useRef(false);
  // A thread a link asked for that the list does not have yet. Before the
  // first list: the page's /thread/<id> URL or an early "open-thread" message
  // from the service worker, which the restore below takes ahead of the last
  // thread. After it: a tap on a thread made elsewhere while this page slept,
  // whose message beats the refreshed list carrying it. Held until the list
  // has it, the user picks something themselves, or a newer link replaces it.
  const pendingLink = useRef<string | null>(wire.link);
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
    (id: string, { history = "push" }: { history?: HistoryWrite } = {}) => {
      pendingLink.current = null;
      // react-doctor-disable-next-line react-doctor/no-adjust-state-on-prop-change -- not derived state: a pick shows its thread, which on a phone means putting the list away
      if (!isDesktop) setSidebarOpen(false);
      // The thread already open stays as it is: no re-attach, no trip
      // through "Attaching…", the panel and scroll left alone.
      if (id === activeRef.current) return;
      // react-doctor-disable-next-line react-doctor/no-adjust-state-on-prop-change -- not derived state: selecting attaches the socket and writes storage, and the restore effect is the one caller that reacts to the thread list
      setCreating(null);
      setActiveId(id);
      activeRef.current = id;
      onLeave();
      threadStore.set(null);
      localStorage.setItem(LAST_THREAD, id);
      clientRef.current?.attach(id);
      if (history !== "none") navigate(threadPath(id), { replace: history === "replace" });
    },
    [activeRef, clientRef, isDesktop, onLeave, threadStore],
  );

  // The draft takes the thread's place: nothing is attached while it is up,
  // and picking a thread from the list leaves it.
  // With no preset (or with one naming no project) the draft opens on the
  // project of the thread being left. The list is read through a ref rather
  // than closed over: the sidebar's New button takes this, and a callback that
  // changed with every threads broadcast would re-render the memoised sidebar
  // for each one.
  const threadsRef = useLatest(threads);
  // The draft's history entry is /, flagged so Forward onto it opens a draft
  // again rather than nothing.
  const startNew = useCallback(
    (preset?: DraftPreset, { history = "push" }: { history?: HistoryWrite } = {}) => {
      pendingLink.current = null;
      const asked = preset ?? {};
      const projectId =
        asked.projectId ?? threadsRef.current.find((t) => t.id === activeRef.current)?.projectId;
      nonce.current += 1;
      setCreating({ ...asked, projectId, nonce: nonce.current });
      if (activeRef.current) {
        activeRef.current = null;
        setActiveId(null);
        threadStore.set(null);
        dropResume();
        clientRef.current?.detach();
      }
      if (!isDesktop) setSidebarOpen(false);
      if (history !== "none") navigate("/", { draft: true, replace: history === "replace" });
    },
    [activeRef, clientRef, dropResume, isDesktop, threadStore, threadsRef],
  );

  // Back to nothing selected: Back onto the / the app opened at. On a phone
  // that is the sidebar, its landing screen.
  const creatingRef = useLatest(creating);
  const goHome = useCallback(() => {
    pendingLink.current = null;
    if (!activeRef.current && !creatingRef.current) return;
    setCreating(null);
    if (activeRef.current) {
      activeRef.current = null;
      setActiveId(null);
      threadStore.set(null);
      dropResume();
      clientRef.current?.detach();
      onLeave();
    }
    if (!isDesktop) setSidebarOpen(true);
  }, [activeRef, clientRef, creatingRef, dropResume, isDesktop, onLeave, threadStore]);

  // The draft's own project pick, kept here so the Skills page reads the
  // project being drafted in and the draft comes back on it. A folder or
  // worktree preset meant the old project, so it goes; the nonce stays, since
  // this is the same draft and must not remount under the person picking.
  const pickDraftProject = useCallback((projectId: string) => {
    setCreating((c) => c && { projectId, nonce: c.nonce });
  }, []);

  // Letting go of the attached thread because it is gone. On a phone that
  // leaves nothing behind the sidebar, so it comes back.
  const release = useCallback(() => {
    // react-doctor-disable-next-line react-doctor/no-adjust-state-on-prop-change -- not derived state: letting go detaches the socket, and only the thread list arriving can say the thread is gone
    setActiveId(null);
    threadStore.set(null);
    dropResume();
    clientRef.current?.detach();
    // react-doctor-disable-next-line react-doctor/no-adjust-state-on-prop-change -- not derived state: letting go detaches the socket, and only the thread list arriving can say the thread is gone
    if (!isDesktop) setSidebarOpen(true);
    // Its entry no longer names it, so Back and a reload don't go looking.
    if (threadIdFromPath(window.location.pathname)) navigate("/", { replace: true });
  }, [clientRef, dropResume, isDesktop, threadStore]);

  // Restore the last thread once the list arrives. This runs once: after it,
  // "no thread selected" is a state the user chose, not one we have yet to
  // resolve, and re-opening the sidebar under them would be wrong.
  //
  // Whatever it settles on, the page's entry is rewritten to name it, in
  // place: the page loaded at one URL and is showing one thing, which is one
  // step of history, not two.
  useEffect(() => {
    if (!threadsLoaded || restoreAttempted.current) return;
    restoreAttempted.current = true;
    const link = pendingLink.current;
    pendingLink.current = null;
    if (link) {
      if (openable(threads, link)) {
        if (link !== activeId) {
          dropResume();
          select(link, { history: "none" });
        }
        restoredUrl(link);
        return;
      }
      // Gone or closed: carry on as if there had been no link.
    }
    if (activeId) {
      // Hydrated from the resume cache before the list could say whether the
      // thread still exists. It usually does; when it doesn't (deleted or
      // closed from elsewhere while the page was dead) let go the same way
      // a live delete would. The seenActive effect below can't: it only acts
      // on threads it saw in a list first.
      // It was hydrated at /, from a cache that does not know the URL.
      if (openable(threads, activeId)) {
        restoredUrl(activeId);
        return;
      }
      // Including the position the cache seeded: the scroll prune only drops
      // threads it saw in a list, and this one never made it into one.
      // react-doctor-disable-next-line react-doctor/no-pass-live-state-to-parent -- not live state: the scroll memory must drop a thread the list says is gone, which only this list arriving can tell
      onForget(activeId);
      release();
      return;
    }
    const last = localStorage.getItem(LAST_THREAD);
    if (last && openable(threads, last)) {
      select(last, { history: "none" });
      restoredUrl(last);
      return;
    }
    restoredUrl(null);
    // Nothing to restore into, so the phone lands on the sidebar after all.
    // react-doctor-disable-next-line react-doctor/no-adjust-state-on-prop-change -- not derived state: the phone's sidebar reopens only once the first thread list shows nothing to restore
    if (!isDesktop) setSidebarOpen(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [threadsLoaded, threads]);

  // A tapped notification, with the app already open: the service worker
  // focuses this window and names the thread. It opens only once the list has
  // it, since attaching to a thread the list lacks would hang on
  // "Attaching…"; until then it waits in pendingLink.
  const openLink = useCallback(
    (id: string) => {
      onOpenLink();
      select(id);
    },
    [onOpenLink, select],
  );
  const latest = useLatest({ threads, openLink, select, startNew, goHome, onOpenLink });
  useEffect(() => {
    const sw = "serviceWorker" in navigator ? navigator.serviceWorker : null;
    if (!sw) return;
    const onMessage = (e: MessageEvent) => {
      const id = openThreadMessage(e.data);
      if (!id) return;
      const { threads, openLink } = latest.current;
      // Before the first list too: the restore has not run, so nothing is
      // openable yet and the restore picks it up.
      if (restoreAttempted.current && openable(threads, id)) openLink(id);
      else pendingLink.current = id;
    };
    sw.addEventListener("message", onMessage);
    return () => sw.removeEventListener("message", onMessage);
  }, [latest]);

  // Back and Forward. The browser has already moved, so nothing here writes
  // history except to say a thread is gone. A sheet's entry (lib/history.ts)
  // is at the open thread's own URL, and popping it lands in the first
  // branch: that one is the sheet's to handle, not ours.
  useEffect(() => {
    const onPop = () => {
      const { pathname } = window.location;
      const { threads, select, startNew, goHome, onOpenLink } = latest.current;
      const id = threadIdFromPath(pathname);
      if (id) {
        if (id === activeRef.current && !creatingRef.current) return;
        // Before the first list nothing is openable yet; the restore takes
        // it, as it does the page's own URL.
        if (!restoreAttempted.current) {
          pendingLink.current = id;
          return;
        }
        if (openable(threads, id)) {
          // Like a tapped notification: whatever covers the thread view goes.
          onOpenLink();
          dropResume();
          select(id, { history: "none" });
          return;
        }
        // Deleted or closed since: nothing to go back to, so the entry stops
        // naming it.
        goHome();
        navigate("/", { replace: true });
        return;
      }
      if (pathname !== "/") return;
      if (isDraftEntry()) {
        if (!creatingRef.current) startNew(undefined, { history: "none" });
        return;
      }
      goHome();
    };
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, [activeRef, creatingRef, dropResume, latest]);

  // The list caught up with a held link. The restore effect above runs first
  // and consumes a link the first list settles, so this only sees later ones.
  useEffect(() => {
    const id = pendingLink.current;
    if (!restoreAttempted.current || !id || !openable(threads, id)) return;
    latest.current.openLink(id);
  }, [threads, latest]);

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
    /** A phone's open list hides the thread behind it. */
    covering: !isDesktop && sidebarOpen,
    setSidebarOpen,
    select,
    startNew,
    pickDraftProject,
  };
}

/**
 * Rewrite the page's entry to name what the restore opened. Only / and a
 * thread's path are the restore's to touch (/setup is a screen of its own),
 * and the query and hash stay: the theme sample is the hash.
 */
function restoredUrl(id: string | null) {
  const { pathname, search, hash } = window.location;
  if (pathname !== "/" && !threadIdFromPath(pathname)) return;
  navigate((id ? threadPath(id) : "/") + search + hash, { replace: true });
}

/** Whether the list has this thread and it can still be opened. */
function openable(threads: ThreadMeta[], id: string): boolean {
  return threads.some((s) => s.id === id && s.phase !== "closed");
}
