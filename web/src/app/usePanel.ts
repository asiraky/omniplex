import { useCallback, useEffect, useState, type RefObject } from "react";

import type { PanelRequest } from "~/components/panel/Panel";
import type { ThreadState } from "~/protocol";

export type PanelControls = ReturnType<typeof usePanel>;

/**
 * Whether the side panel is showing, how wide, and what it was last asked to
 * put on screen. Every "open this" in the thread (a turn's diff card, a path in
 * prose, a jobs strip, an artefact card) comes through here.
 */
export function usePanel(stateRef: RefObject<ThreadState | null>) {
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
  // What the panel should put on screen, and a counter that changes on every
  // request. Without the counter, asking for the same file twice would look
  // identical to the panel and it would not bring it back into view.
  const [request, setRequest] = useState<PanelRequest | null>(null);

  // Opening the diff from a turn's card: show the panel, and put it on the file
  // that was clicked.
  const openDiff = useCallback((path?: string) => {
    setOpen(true);
    setRequest((current) => ({ kind: "diff", path, nonce: (current?.nonce ?? 0) + 1 }));
  }, []);

  // Opening the jobs surface from the strip or a spawn card in the transcript.
  const openJobs = useCallback(() => {
    setOpen(true);
    setRequest((current) => ({ kind: "jobs", nonce: (current?.nonce ?? 0) + 1 }));
  }, []);

  // Opening an artefact from its card in the transcript.
  const openArtefact = useCallback((artefactId: string) => {
    setOpen(true);
    setRequest((current) => ({ kind: "artefact", artefactId, nonce: (current?.nonce ?? 0) + 1 }));
  }, []);

  // Opening a path clicked in prose. The panel routes it: the diff surface
  // when the thread changed it, the file surface otherwise. An absolute path
  // under the checkout is relativised first; the server only serves the
  // workspace.
  const openPath = useCallback(
    (path: string, line?: number) => {
      const cwd = stateRef.current?.cwd ?? "";
      let rel = path;
      if (cwd && (rel === cwd || rel.startsWith(cwd + "/")))
        rel = rel.slice(cwd.length).replace(/^\//, "");
      if (rel === "") return;
      setOpen(true);
      setRequest((current) => ({
        kind: "path",
        path: rel,
        line,
        nonce: (current?.nonce ?? 0) + 1,
      }));
    },
    [stateRef],
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
