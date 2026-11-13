import { lazy, Suspense, useCallback, type RefObject } from "react";

import { useThread, useThreadStore } from "~/app/threadStore";
import type { PanelControls } from "~/app/usePanel";
import type { Client } from "~/client";
import type { OpenPath } from "~/lib/openPath";
import { pathInCheckout } from "~/lib/paths";
import { toast } from "~/lib/toast";
import type {
  DiffComparison,
  FileContent,
  FileDiff,
  FileTree,
  PullRequest,
  ThreadChanges,
  ThreadState,
} from "~/protocol";

const Panel = lazy(() => import("./panel/Panel").then((m) => ({ default: m.Panel })));

/**
 * The side panel for the attached thread, and the commands it reads the
 * thread's checkout through. Nothing loads until the panel is first opened.
 */
export function ThreadPanel({
  clientRef,
  threadId,
  panel,
  pr,
  onAddToChat,
}: {
  clientRef: RefObject<Client | null>;
  threadId: string;
  panel: PanelControls;
  pr: PullRequest | null;
  /** Terminal output quoted for the composer. */
  onAddToChat: (quoted: string) => void;
}) {
  const threadStore = useThreadStore();

  const command = useCallback(
    (cmd: string, args: unknown) => clientRef.current!.command(cmd, args),
    [clientRef],
  );

  // Git is the source of truth for what a thread changed: it catches the
  // formatter and the codemod as well as the edits we parsed out of tool calls.
  const loadChanges = useCallback(
    async (comparison: DiffComparison, turnId?: string) => {
      const res = await clientRef.current!.command("thread_changes", { threadId, comparison, turnId });
      return res.changes as ThreadChanges;
    },
    [clientRef, threadId],
  );

  const loadDiff = useCallback(
    async (path: string, changes: ThreadChanges) => {
      const res = await clientRef.current!.command("thread_file_diff", {
        threadId,
        path,
        comparison: changes.mode,
        turnId: changes.turnId,
        base: changes.base,
        head: changes.head,
      });
      return res.diff as FileDiff;
    },
    [clientRef, threadId],
  );

  // The real filesystem, for the files and file surfaces: git is the diff
  // surface, and a file the thread never touched is exactly what it can't show.
  const loadTree = useCallback(
    async (includeIgnored: boolean) => {
      const res = await clientRef.current!.command("thread_file_tree", {
        threadId,
        includeIgnored,
      });
      return res.tree as FileTree;
    },
    [clientRef, threadId],
  );

  const loadFile = useCallback(
    async (path: string) => {
      const res = await clientRef.current!.command("thread_read_file", { threadId, path });
      return res.file as FileContent;
    },
    [clientRef, threadId],
  );

  // The jobs a snapshot left out. The client merges and publishes them, so the
  // roster picks them up through state like any other update.
  const loadAllJobs = useCallback(
    async () => (await clientRef.current?.loadAllJobs()) ?? null,
    [clientRef],
  );

  // A path a terminal printed. The terminal hands over one under the shell's
  // start directory, the checkout root, already relative to it; anything else
  // is absolute, or as printed when the shell could not say where it was. The
  // panel can only show the checkout, and a shell wanders: `~/.zshrc`,
  // `/etc/hosts` and `../other-repo` get a word rather than a file surface
  // that fails to load. An absolute path is tried against the thread's own
  // working directory: right when the shell could not answer and the thread
  // sits at the root, and it catches a path spelled through a symlink the
  // shell's answer resolved. Read at the click, so the callback stays the same
  // across frames.
  const { openPath } = panel;
  const openTerminalPath = useCallback<OpenPath>(
    (path, line) => {
      const rel = pathInCheckout(path, threadStore.get()?.cwd ?? "");
      if (rel === null) toast.info("That file is outside this thread's checkout");
      else openPath(rel, line);
    },
    [threadStore, openPath],
  );

  // All this reads of the thread, so the frames of a turn leave it alone.
  const inTurn = useThread(threadStore, isInTurn);

  if (!panel.loaded) return null;
  return (
    <Suspense fallback={null}>
      <Panel
        // Remounted per thread: the tab model is per-thread state.
        key={threadId}
        threadId={threadId}
        command={command}
        loadAllJobs={loadAllJobs}
        open={panel.open}
        onClose={panel.close}
        expanded={panel.expanded}
        onToggleExpanded={panel.toggleExpanded}
        // The worktree is worth re-reading when the agent stops writing to it.
        revision={`${threadId}:${inTurn ? "turn" : "settled"}`}
        loadChanges={loadChanges}
        loadDiff={loadDiff}
        loadTree={loadTree}
        loadFile={loadFile}
        request={panel.request}
        onRequestRouted={panel.requestRouted}
        pr={pr}
        onAddToChat={onAddToChat}
        onOpenPath={openTerminalPath}
      />
    </Suspense>
  );
}

const isInTurn = (state: ThreadState | null) => state?.phase === "turn";
