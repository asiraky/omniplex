import type { ReactNode } from "react";

import type { DraftPreset } from "~/app/useThreadSelection";
import { folderName } from "~/components/threadDraft/shared";
import { CommandEmpty, CommandGroup } from "~/components/ui/command";
import type { Folder, Project, Workspace } from "~/protocol";

import type { NewThreadStep } from "./newThread";
import { PaletteRow } from "./PaletteRow";
import { matchScore } from "./search";
import type { WorkspaceListing } from "./useWorkspaces";

/** The step's question, as the search field's placeholder. */
export const STEP_PROMPT: Record<NewThreadStep["kind"], string> = {
  project: "Which project?",
  folder: "Which folder?",
  workspace: "Which checkout?",
};

/** The checkout's name in the list: its branch, else the commit it sits on. */
function worktreeLabel(w: Workspace) {
  return w.branch || (w.head ? `detached @ ${w.head}` : folderName(w.path));
}

/** The last two segments of a path: enough to tell worktrees apart. */
function shortPath(path: string) {
  return path.replace(/\/+$/, "").split("/").slice(-2).join("/");
}

/**
 * The rows of one step of "New thread in…", narrowed by the query. Every step
 * is a list the keyboard walks, so the flow from project to worktree never
 * needs the pointer.
 */
export function NewThreadPage({
  step,
  query,
  projects,
  listing,
  onPickProject,
  onPickFolder,
  onPickWorkspace,
}: {
  step: NewThreadStep;
  query: string;
  /** In the order to list them: the default project first. */
  projects: Project[];
  listing: WorkspaceListing;
  onPickProject: (project: Project) => void;
  onPickFolder: (folderId: string) => void;
  onPickWorkspace: (choice: Pick<DraftPreset, "kind" | "attachPath">) => void;
}) {
  const hit = (text: string, keywords?: string[]) => matchScore(query, text, keywords) > 0;
  let rows: ReactNode[] = [];
  let extra: ReactNode = null;

  if (step.kind === "project") {
    rows = projects
      .filter((p) => hit(p.name))
      .map((p) => (
        <PaletteRow
          key={p.id}
          value={`project:${p.id}`}
          label={p.name}
          opensStep={p.folders.length > 1 || !!p.folders[0]?.git}
          onSelect={() => onPickProject(p)}
        />
      ));
  }

  if (step.kind === "folder") {
    // Listed in the order the draft's scope menu uses, whole project first.
    const options: { id: string; label: string; detail: string; folder?: Folder }[] = [
      { id: "", label: "Everything", detail: "Every folder, worked on directly" },
      ...step.project.folders.map((f) => ({
        id: f.id,
        label: folderName(f.path),
        detail: f.path,
        folder: f,
      })),
    ];
    rows = options
      .filter((o) => hit(o.label, o.folder ? [o.folder.path] : ["whole project", "all"]))
      .map((o) => (
        <PaletteRow
          key={o.id || "everything"}
          value={`folder:${o.id}`}
          label={o.label}
          detail={o.detail}
          opensStep={!!o.folder?.git}
          onSelect={() => onPickFolder(o.id)}
        />
      ));
  }

  if (step.kind === "workspace") {
    const { folder } = step;
    if (hit("Main checkout", ["root", folder.path]))
      rows.push(
        <PaletteRow
          key="main"
          value="workspace:main"
          label="Main checkout"
          detail={folder.baseBranch ? `${folder.baseBranch} · ${folder.path}` : folder.path}
          onSelect={() => onPickWorkspace({ kind: "main" })}
        />,
      );
    if (hit("New worktree", ["branch", "create"]))
      rows.push(
        <PaletteRow
          key="branch"
          value="workspace:branch"
          label="New worktree"
          detail="New branch, named for you"
          onSelect={() => onPickWorkspace({ kind: "branch" })}
        />,
      );
    if (listing.state === "ready") {
      // Every checkout is pickable, busy or not, as in the draft's own
      // picker: sharing one is a warning, not something Git forbids.
      const existing = listing.workspaces
        .filter((w) => !w.isRoot && hit(worktreeLabel(w), [w.path]))
        .map((w) => (
          <PaletteRow
            key={w.path}
            value={`worktree:${w.path}`}
            label={worktreeLabel(w)}
            detail={shortPath(w.path)}
            note={w.busy ? (w.busyTitle ? `in use: ${w.busyTitle}` : "in use") : w.locked ? "locked" : undefined}
            onSelect={() => onPickWorkspace({ kind: "attach", attachPath: w.path })}
          />
        ));
      if (existing.length > 0)
        extra = (
          <CommandGroup heading="Existing worktrees">{existing}</CommandGroup>
        );
    } else {
      extra = (
        <p
          role={listing.state === "error" ? "alert" : "status"}
          className={
            listing.state === "error"
              ? "text-destructive px-3 py-2 text-xs"
              : "text-muted-foreground px-3 py-2 text-xs"
          }
        >
          {listing.state === "error"
            ? `Could not list worktrees: ${listing.message}`
            : "Loading worktrees…"}
        </p>
      );
    }
  }

  return (
    <>
      {step.kind === "project" && projects.length === 0 ? (
        <p className="text-muted-foreground py-6 text-center text-sm">No projects yet.</p>
      ) : (
        <CommandEmpty>Nothing matches that.</CommandEmpty>
      )}
      {rows.length > 0 && <CommandGroup>{rows}</CommandGroup>}
      {extra}
    </>
  );
}
