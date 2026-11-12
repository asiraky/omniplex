import { useEffect, useState } from "react";

import type { Workspace } from "~/protocol";
import { useLatest } from "~/useLatest";

export type WorkspaceListing =
  | { state: "loading" }
  | { state: "ready"; workspaces: Workspace[] }
  | { state: "error"; message: string };

/**
 * The worktrees of one folder, asked for when the workspace step opens. A
 * stale answer — the user stepped back and picked another folder before it
 * landed — is dropped, and the listing reads as loading until the right one
 * arrives. `null` is no folder to ask about.
 */
export function useWorkspaces(
  list: (projectId: string, folderId: string) => Promise<Workspace[]>,
  scope: { projectId: string; folderId: string } | null,
): WorkspaceListing {
  const latest = useLatest(list);
  const key = scope ? `${scope.projectId}\0${scope.folderId}` : "";
  const [result, setResult] = useState<{ key: string; listing: WorkspaceListing } | null>(null);

  useEffect(() => {
    if (!key) return;
    const [projectId, folderId] = key.split("\0");
    let live = true;
    latest.current(projectId, folderId).then(
      (workspaces) => live && setResult({ key, listing: { state: "ready", workspaces } }),
      (err: unknown) =>
        live &&
        setResult({
          key,
          listing: { state: "error", message: err instanceof Error ? err.message : String(err) },
        }),
    );
    return () => {
      live = false;
    };
  }, [key, latest]);

  return result && result.key === key ? result.listing : { state: "loading" };
}
