import { lazy, Suspense, useCallback, type RefObject } from "react";

import type { PanelControls } from "~/app/usePanel";
import type { Client } from "~/client";
import type { Skill } from "~/lib/skills";
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
  state,
  panel,
  pr,
  onUseSkill,
}: {
  clientRef: RefObject<Client | null>;
  threadId: string;
  state: ThreadState;
  panel: PanelControls;
  pr: PullRequest | null;
  /** Puts a skill's token into this thread's composer. */
  onUseSkill?: (skill: Skill) => void | Promise<void>;
}) {
  const command = useCallback(
    (cmd: string, args: unknown) => clientRef.current!.command(cmd, args),
    [clientRef],
  );

  // Git is the source of truth for what a thread changed: it catches the
  // formatter and the codemod as well as the edits we parsed out of tool calls.
  const loadChanges = useCallback(
    async (comparison: DiffComparison) => {
      const res = await clientRef.current!.command("thread_changes", { threadId, comparison });
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

  if (!panel.loaded) return null;
  return (
    <Suspense fallback={null}>
      <Panel
        // Remounted per thread: the tab model is per-thread state.
        key={threadId}
        threadId={threadId}
        state={state}
        command={command}
        open={panel.open}
        onClose={panel.close}
        expanded={panel.expanded}
        onToggleExpanded={panel.toggleExpanded}
        // The worktree is worth re-reading when the agent stops writing to it.
        revision={`${threadId}:${state.phase === "turn" ? "turn" : "settled"}`}
        loadChanges={loadChanges}
        loadDiff={loadDiff}
        loadTree={loadTree}
        loadFile={loadFile}
        request={panel.request}
        pr={pr}
        onUseSkill={onUseSkill}
      />
    </Suspense>
  );
}
