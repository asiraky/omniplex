import { useCallback, useEffect, useRef, useState } from "react";

import type { PanelRequest } from "~/components/panel/Panel";
import type { DiffTurn } from "~/protocol";

import type { ThreadStore } from "./threadStore";

export type PanelControls = ReturnType<typeof usePanel>;

/**
 * Whether the side panel is showing, how wide, and what it was last asked to
 * put on screen. Every "open this" in the thread (a turn's diff card, a path in
 * prose, a jobs strip, an artefact card) comes through here.
 */
export function usePanel(threadStore: ThreadStore) {
  const [open, setOpen] = useState(false);
  // The panel is lazy: nothing is fetched until it is first opened, and after
  // that it stays mounted so closing and reopening it is instant.
  const [loaded, setLoaded] = useState(false);
  useEffect(() => {
    if (open) setLoaded(true);
  }, [open]);
  // One click takes the diff panel to the full content width; another brings
  // it back. There is no in-between state on purpose.
  const [expanded, setExpanded] = useState(false);
  // What the panel should put on screen, held until the panel has routed it.
  // The panel's body remounts (a phone's sheet closing, the 1024px breakpoint,
  // a reconnect) and this outlives it, so a request it kept after routing would
  // be replayed by every mount, reopening a tab the user had since closed. One
  // made while the body is unmounted waits here for the mount that routes it.
  // The nonce changes on every request, so asking for the same file twice
  // still brings it back into view; it never repeats, so a body that routed
  // nonce 3 cannot mistake a later request for one it has already seen.
  const [request, setRequest] = useState<PanelRequest | null>(null);
  const nonces = useRef(0);
  const ask = useCallback((next: Omit<PanelRequest, "nonce">) => {
    setOpen(true);
    setRequest({ ...next, nonce: ++nonces.current });
  }, []);
  // The panel has put this nonce on screen. A newer request is left alone.
  const requestRouted = useCallback((nonce: number) => {
    setRequest((current) => (current?.nonce === nonce ? null : current));
  }, []);

  // Opening the diff from a turn's card: show the panel, and put it on the file
  // that was clicked. A card names its turn, so the panel shows what that turn
  // changed; the keybinding and palette name none and leave the comparison be.
  const openDiff = useCallback((path?: string, turn?: DiffTurn) => ask({ kind: "diff", path, turn }), [ask]);

  // Opening the jobs surface from the strip or the transcript's agents card:
  // on the roster, or on the one job a card's row names.
  const openJobs = useCallback((jobId?: string) => ask({ kind: "jobs", jobId }), [ask]);

  // Opening an artefact from its card in the transcript.
  const openArtefact = useCallback((artefactId: string) => ask({ kind: "artefact", artefactId }), [ask]);

  // Opening a path clicked in prose. The panel routes it: the diff surface
  // when the thread changed it, the file surface otherwise. An absolute path
  // under the checkout is relativised first; the server only serves the
  // workspace. The checkout is read at the click, so the callback stays the
  // same across frames.
  const openPath = useCallback(
    (path: string, line?: number) => {
      const cwd = threadStore.get()?.cwd ?? "";
      let rel = path;
      if (cwd && (rel === cwd || rel.startsWith(cwd + "/")))
        rel = rel.slice(cwd.length).replace(/^\//, "");
      if (rel === "") return;
      ask({ kind: "path", path: rel, line });
    },
    [threadStore, ask],
  );

  // The panel belongs to a checkout, so it must not survive a move to a
  // different one. A file asked for in one thread means nothing in the next,
  // and another thread holding the same path would otherwise open it unasked.
  const reset = useCallback(() => {
    setOpen(false);
    setExpanded(false);
    setRequest(null);
  }, []);

  const close = useCallback(() => {
    setOpen(false);
    setExpanded(false);
  }, []);
  const toggle = useCallback(() => setOpen((v) => !v), []);
  const toggleExpanded = useCallback(() => setExpanded((v) => !v), []);
  const show = useCallback(() => setOpen(true), []);

  return {
    open,
    loaded,
    expanded,
    request,
    requestRouted,
    show,
    toggle,
    close,
    toggleExpanded,
    reset,
    openDiff,
    openJobs,
    openArtefact,
    openPath,
  };
}
