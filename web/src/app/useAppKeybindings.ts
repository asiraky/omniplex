import type { RefObject } from "react";

import type { ComposerHandle } from "~/components/Composer";
import { COMMAND_IDS, DIFF_FIND_EVENT, jumpIndex } from "~/lib/keybindings";
import type { Project, ThreadMeta } from "~/protocol";
import { sidebarOrder, type SidebarView } from "~/sidebarOrder";
import { rowStatus } from "~/threadStatus";

import { focusedDiff, useKeybindings, type AppKeyState, type KeyHandlers } from "./useKeybindings";

/** Wants a look: failed, unread since it stopped, or waiting on an answer. */
export function needsAttention(s: ThreadMeta): boolean {
  const status = rowStatus(s);
  return (
    status === "failed" ||
    status === "new" ||
    s.attention === "needs_permission" ||
    s.attention === "needs_answer"
  );
}

/** One step through `order` from `activeId`, wrapping; nothing active starts at an end. */
export function stepThread(order: ThreadMeta[], activeId: string | null, by: 1 | -1): string | null {
  if (order.length === 0) return null;
  const at = order.findIndex((t) => t.id === activeId);
  if (at < 0) return order[by === 1 ? 0 : order.length - 1].id;
  return order[(at + by + order.length) % order.length].id;
}

/** The first thread after the active one, wrapping, that needs attention; never the active one. */
export function nextAttention(order: ThreadMeta[], activeId: string | null): string | null {
  const at = order.findIndex((t) => t.id === activeId);
  for (let i = 1; i <= order.length; i++) {
    const t = order[(at + i + order.length) % order.length];
    if (t.id !== activeId && needsAttention(t)) return t.id;
  }
  return null;
}

/**
 * Every shortcut, wired to what the app already does from a button. Mounted
 * once in App, ahead of its early returns.
 */
export function useAppKeybindings({
  app,
  threads,
  projects,
  view,
  activeId,
  composerRef,
  onOpenThread,
  onTogglePalette,
  onNewThreadIn,
  onNewThreadHere,
  onCopyTranscript,
  onFindInThread,
  onStopTurn,
  onToggleSidebar,
  onTogglePanel,
  onOpenDiff,
  onClosePanel,
  onSettings,
  onToggleHelp,
}: {
  app: AppKeyState;
  threads: ThreadMeta[];
  projects: Project[];
  view: SidebarView;
  activeId: string | null;
  composerRef: RefObject<ComposerHandle | null>;
  /** Opens a thread from wherever the user is, leaving any full-page screen. */
  onOpenThread: (id: string) => void;
  onTogglePalette: () => void;
  onNewThreadIn: () => void;
  onNewThreadHere: () => void;
  onCopyTranscript: () => void;
  onFindInThread: () => void;
  onStopTurn: () => void;
  onToggleSidebar: () => void;
  onTogglePanel: () => void;
  onOpenDiff: () => void;
  onClosePanel: () => void;
  onSettings: () => void;
  onToggleHelp: () => void;
}) {
  const go = (id: string | null) => {
    if (id && id !== activeId) onOpenThread(id);
  };
  // Read at the moment of the key, not per render: the walk is the list as
  // the sidebar shows it right then.
  const shown = () => sidebarOrder(threads, projects, view);

  const handlers: KeyHandlers = {
    palette: onTogglePalette,
    newThreadIn: onNewThreadIn,
    newThreadHere: onNewThreadHere,
    prevThread: () => go(stepThread(shown(), activeId, -1)),
    nextThread: () => go(stepThread(shown(), activeId, 1)),
    // Folded sections count: a thread waiting on an answer is worth opening
    // a group for.
    nextAttention: () =>
      go(nextAttention(sidebarOrder(threads, projects, view, { includeCollapsed: true }), activeId)),
    copyTranscript: onCopyTranscript,
    findInThread: onFindInThread,
    // The surface holding focus opens its own find bar; there can be more
    // than one mounted (docked and expanded), so it is asked, not looked up.
    findInDiff: () =>
      focusedDiff()?.dispatchEvent(new CustomEvent(DIFF_FIND_EVENT)),
    stopTurn: onStopTurn,
    stopTurnEscape: onStopTurn,
    focusComposer: () => composerRef.current?.focusEnd(),
    sendAndNewThread: () => composerRef.current?.sendAndNewThread(),
    modelPicker: () => composerRef.current?.openModelPicker("model"),
    effort: () => composerRef.current?.openModelPicker("effort"),
    toggleSidebar: onToggleSidebar,
    togglePanel: onTogglePanel,
    openDiff: onOpenDiff,
    // Claimed either way, so ⌘W never closes the desktop app's window.
    closePanel: onClosePanel,
    settings: onSettings,
    shortcutsHelp: onToggleHelp,
  };
  for (const id of COMMAND_IDS) {
    const i = jumpIndex(id);
    if (i !== null) handlers[id] = () => go(shown()[i]?.id ?? null);
  }

  useKeybindings(handlers, app);
}
