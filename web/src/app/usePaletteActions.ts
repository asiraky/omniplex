import type { RefObject } from "react";

import type { PaletteAction } from "~/components/CommandPalette";
import type { ComposerHandle } from "~/components/Composer";
import { useTheme, type Theme } from "~/lib/theme";
import type { ThreadMeta } from "~/protocol";
import { unread } from "~/threadStatus";

const THEMES: { value: Theme; label: string }[] = [
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
  { value: "system", label: "System" },
];

/**
 * What the palette can do beyond threads: to the open thread, then to the
 * app. Built fresh each render from what is on screen, so a row is only there
 * when it would do something.
 */
export function usePaletteActions({
  meta,
  threadOpen,
  turnRunning,
  composerOpen,
  composerRef,
  onSetPinned,
  onSetUnread,
  onCopyTranscript,
  onDelete,
  onTogglePanel,
  onOpenDiff,
  onStopTurn,
  onNewThreadHere,
  onNewProject,
  onSettings,
  onUsage,
  onSkills,
  onToggleSidebar,
  onShortcuts,
}: {
  /** The open thread, once the list has it. */
  meta: ThreadMeta | undefined;
  threadOpen: boolean;
  turnRunning: boolean;
  /** A composer is on screen: the open thread's or a draft's. */
  composerOpen: boolean;
  composerRef: RefObject<ComposerHandle | null>;
  onSetPinned: (threadId: string, pinned: boolean) => void;
  onSetUnread: (threadId: string, unread: boolean) => void;
  onCopyTranscript: () => void;
  onDelete: (meta: ThreadMeta) => void;
  onTogglePanel: () => void;
  onOpenDiff: () => void;
  onStopTurn: () => void;
  onNewThreadHere: () => void;
  onNewProject: () => void;
  onSettings: () => void;
  onUsage: () => void;
  onSkills: () => void;
  onToggleSidebar: () => void;
  onShortcuts: () => void;
}): PaletteAction[] {
  const { theme, setTheme } = useTheme();
  const actions: PaletteAction[] = [];
  const thread = (a: Omit<PaletteAction, "group">) => actions.push({ ...a, group: "Thread" });
  const app = (a: Omit<PaletteAction, "group">) => actions.push({ ...a, group: "App" });

  if (threadOpen && meta) {
    const s = meta;
    thread({
      id: "pin",
      label: s.pinned ? "Unpin thread" : "Pin thread",
      keywords: ["pin", "top"],
      run: () => onSetPinned(s.id, !s.pinned),
    });
    // As the row's menu has it: a thread whose log is empty has nothing to be
    // unread about.
    if (unread(s)) {
      thread({ id: "read", label: "Mark read", keywords: ["seen"], run: () => onSetUnread(s.id, false) });
    } else if (s.headSeq > 0) {
      thread({
        id: "unread",
        label: "Mark unread",
        keywords: ["later", "new"],
        run: () => onSetUnread(s.id, true),
      });
    }
  }
  if (threadOpen) {
    thread({
      id: "copy",
      label: "Copy transcript",
      command: "copyTranscript",
      keywords: ["markdown", "clipboard"],
      run: onCopyTranscript,
    });
    if (meta) {
      const s = meta;
      thread({ id: "delete", label: "Delete thread…", keywords: ["remove", "finish"], run: () => onDelete(s) });
    }
    thread({
      id: "panel",
      label: "Toggle side panel",
      command: "togglePanel",
      keywords: ["terminal", "files", "jobs"],
      run: onTogglePanel,
    });
    thread({ id: "diff", label: "Open diff", command: "openDiff", keywords: ["changes"], run: onOpenDiff });
    if (turnRunning) {
      thread({
        id: "stop",
        label: "Stop the turn",
        command: "stopTurn",
        keywords: ["cancel", "interrupt"],
        run: onStopTurn,
      });
    }
  }
  if (composerOpen) {
    // The palette is still closing when an action runs, and opening the
    // picker under a closing dialog would have the dialog's dismissal close
    // it again. A frame later it is gone, as with the composer's /model.
    const picker = (focus: "model" | "effort") => () =>
      window.requestAnimationFrame(() => composerRef.current?.openModelPicker(focus));
    thread({
      id: "model",
      label: "Model picker",
      command: "modelPicker",
      keywords: ["harness", "switch"],
      run: picker("model"),
    });
    thread({
      id: "effort",
      label: "Reasoning effort",
      command: "effort",
      keywords: ["thinking"],
      run: picker("effort"),
    });
  }

  app({
    id: "new-here",
    label: "New thread in this project",
    command: "newThreadHere",
    keywords: ["create", "start"],
    run: onNewThreadHere,
  });
  app({ id: "new-project", label: "New project", keywords: ["add", "repository"], run: onNewProject });
  app({ id: "settings", label: "Settings", command: "settings", keywords: ["preferences"], run: onSettings });
  app({ id: "usage", label: "Usage", keywords: ["cost", "tokens", "limits"], run: onUsage });
  app({ id: "skills", label: "Skills", keywords: ["tools", "mcp"], run: onSkills });
  app({
    id: "sidebar",
    label: "Toggle threads",
    command: "toggleSidebar",
    keywords: ["sidebar", "list"],
    run: onToggleSidebar,
  });
  for (const t of THEMES) {
    if (t.value === theme) continue;
    app({
      id: `theme-${t.value}`,
      label: `Theme: ${t.label}`,
      keywords: ["appearance", "mode"],
      run: () => setTheme(t.value),
    });
  }
  app({
    id: "shortcuts",
    label: "Keyboard shortcuts",
    command: "shortcutsHelp",
    keywords: ["keys", "help", "hotkeys"],
    run: onShortcuts,
  });

  return actions;
}
