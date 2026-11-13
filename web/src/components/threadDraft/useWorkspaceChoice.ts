import { useState } from "react";

import type { WorkspaceChoice } from "~/components/WorkspacePicker";
import type { ProjectPrefs } from "~/lib/threadPrefs";
import type { Folder, Issue, Project, Workspace } from "~/protocol";
import { folderName, type WorkspaceKind } from "./shared";
import { useFolderListing } from "./useFolderListing";

export interface IssueListing {
  issues: Issue[];
  issuesError: string;
}

/** The workspace a draft was asked to open on, in the project named (or any). */
export interface WorkspacePreset {
  projectId?: string;
  kind?: WorkspaceKind;
  attachPath?: string;
}

const NO_CHOICE: WorkspaceChoice = { branch: "", attachPath: "" };
const NO_WORKSPACES: Workspace[] = [];
const NO_ISSUES: IssueListing = { issues: [], issuesError: "" };
const noWorkspaces = () => NO_WORKSPACES;
const presetChoice = (preset: WorkspacePreset): WorkspaceChoice => ({
  branch: "",
  attachPath: preset.kind === "attach" ? (preset.attachPath ?? "") : "",
});
const issuesFailed = (e: unknown): IssueListing => ({
  issues: [],
  issuesError: e instanceof Error ? e.message : String(e),
});

/** What the Git chip says: where the thread will work. */
function labelFor(kind: WorkspaceKind, choice: WorkspaceChoice, attachable: Workspace[]) {
  if (kind === "main") return "Main checkout";
  if (kind === "attach") {
    const attached = attachable.find((w) => w.path === choice.attachPath);
    return attached
      ? `Worktree: ${attached.branch || folderName(attached.path)}`
      : "Existing worktree";
  }
  const branch = choice.branch.trim();
  return branch ? `New worktree: ${branch}` : "New worktree";
}

/** The workspace fields a new thread is created with. */
function sentFor(kind: WorkspaceKind, choice: WorkspaceChoice, baseRef: string) {
  if (kind === "main") return { branch: "", workspace: "local", workspacePath: "", baseRef: "" };
  if (kind === "attach")
    return { branch: "", workspace: "", workspacePath: choice.attachPath, baseRef: "" };
  // A base only means anything where Omniplex is the one creating the branch.
  return {
    branch: choice.branch.trim(),
    workspace: "managed",
    workspacePath: "",
    baseRef: baseRef.trim(),
  };
}

/**
 * Which checkout of a git folder the thread works in: the main checkout, a new
 * worktree (optionally with a named branch and base), or a worktree that
 * already exists. Starts on whatever the project last did, or on the preset
 * the draft was asked for.
 */
export function useWorkspaceChoice({
  project,
  gitScope,
  remembered,
  onListWorkspaces,
  onListIssues,
  preset,
  forceWorktree = false,
}: {
  project: Project | undefined;
  gitScope: Folder | undefined;
  remembered: ProjectPrefs | undefined;
  onListWorkspaces: (projectId: string, folderId: string) => Promise<Workspace[]>;
  onListIssues: (projectId: string, folderId: string) => Promise<IssueListing>;
  /** Read on mount only. */
  preset?: WorkspacePreset;
  /**
   * Comparing models: each thread gets its own new worktree, whatever was
   * picked or last used. The picked kind is kept for when this lifts.
   */
  forceWorktree?: boolean;
}) {
  // A preset belongs to the scope the draft opens on. Mounted before the
  // project list, there is no scope yet and it waits for the first one; after
  // that it is spent, so a folder or project picked later starts over as usual.
  const fits = (p: WorkspacePreset | undefined): p is WorkspacePreset =>
    !!p && !!project && (!p.projectId || p.projectId === project.id);
  const [pending, setPending] = useState(project ? undefined : preset);
  const initial = fits(preset) ? preset : undefined;
  const [choice, setChoice] = useState<WorkspaceChoice>(() =>
    initial ? presetChoice(initial) : NO_CHOICE,
  );
  // "" defers to what the project last used.
  const [chosenKind, setChosenKind] = useState<"" | WorkspaceKind>(initial?.kind ?? "");
  const [naming, setNaming] = useState(false);
  const [baseRef, setBaseRef] = useState("");
  // The branch typed while comparing holds a worktree the person did not pick,
  // so it is kept apart from their own choice, which is there again unchanged
  // once the lock lifts.
  const [lockChoice, setLockChoice] = useState<WorkspaceChoice>(NO_CHOICE);

  // Every choice here belongs to one git folder, so another folder starts
  // over rather than carrying a branch or a worktree that means nothing there.
  const scopeKey = JSON.stringify([project?.id, gitScope?.id]);
  const [choiceScope, setChoiceScope] = useState(scopeKey);
  if (choiceScope !== scopeKey) {
    setChoiceScope(scopeKey);
    const late = fits(pending) ? pending : undefined;
    if (project) setPending(undefined);
    setChoice(late ? presetChoice(late) : NO_CHOICE);
    setLockChoice(NO_CHOICE);
    setChosenKind(late?.kind ?? "");
    setBaseRef("");
    setNaming(false);
  }

  // Worktrees and issues belong to a git folder, re-read whenever the scope
  // changes so a stale list cannot offer a worktree that has since gone.
  const spaces = useFolderListing(
    onListWorkspaces,
    project?.id,
    gitScope?.id,
    NO_WORKSPACES,
    noWorkspaces,
  );
  // `gh` may take seconds to answer and nothing here waits on it.
  const issues = useFolderListing(onListIssues, project?.id, gitScope?.id, NO_ISSUES, issuesFailed);

  const workspaces = spaces.value;
  // The main checkout is its own choice, so it is not offered again as a worktree.
  const attachable = workspaces.filter((w) => !w.isRoot);
  // A worktree a preset named may be gone by the time the list lands, and is
  // not sent on then: the draft waits for one to be picked instead. One picked
  // here always came from the list.
  const listed =
    !choice.attachPath || spaces.loading || attachable.some((w) => w.path === choice.attachPath);
  const own = listed ? choice : { ...choice, attachPath: "" };
  const lastWorktree = remembered?.copy ?? project?.defaults.workspace === "managed";
  const forced = !!gitScope && forceWorktree;
  const ownKind: WorkspaceKind = !gitScope
    ? "main"
    : chosenKind || (lastWorktree ? "branch" : "main");
  const kind: WorkspaceKind = forced ? "branch" : ownKind;
  // A new worktree chosen before comparing is already this kind, so its branch carries over.
  const locked = forced && ownKind !== "branch";
  const current = locked ? lockChoice : own;
  // Branches already on disk are the useful bases: stacking on another worktree's
  // work is what the field is for.
  const baseChoices = Array.from(
    new Set(workspaces.map((w) => w.branch).filter((b): b is string => !!b)),
  ).filter((b) => b !== gitScope?.baseBranch);

  return {
    kind,
    /** Held on a new worktree while models are compared. */
    forced,
    label: labelFor(kind, current, attachable),
    choice: current,
    naming,
    baseRef,
    attachable,
    /** The branch the main checkout is on, once the worktree list is in. */
    mainBranch: workspaces.find((w) => w.isRoot)?.branch ?? "",
    baseChoices,
    issues: issues.value,
    loadingSpaces: spaces.loading,
    loadingIssues: issues.loading,
    /** What the thread is created with. */
    sent: sentFor(kind, current, baseRef),
    setChoice: locked ? setLockChoice : setChoice,
    setNaming,
    setBaseRef,
    /** A different kind of workspace starts its choice over. */
    pickKind: (next: WorkspaceKind) => {
      setChosenKind(next);
      setChoice(NO_CHOICE);
    },
  };
}

export type WorkspaceChoiceState = ReturnType<typeof useWorkspaceChoice>;
