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

const NO_CHOICE: WorkspaceChoice = { branch: "", attachPath: "" };
const NO_WORKSPACES: Workspace[] = [];
const NO_ISSUES: IssueListing = { issues: [], issuesError: "" };
const noWorkspaces = () => NO_WORKSPACES;
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
 * already exists. Starts on whatever the project last did.
 */
export function useWorkspaceChoice({
  project,
  gitScope,
  remembered,
  onListWorkspaces,
  onListIssues,
}: {
  project: Project | undefined;
  gitScope: Folder | undefined;
  remembered: ProjectPrefs | undefined;
  onListWorkspaces: (projectId: string, folderId: string) => Promise<Workspace[]>;
  onListIssues: (projectId: string, folderId: string) => Promise<IssueListing>;
}) {
  const [choice, setChoice] = useState<WorkspaceChoice>(NO_CHOICE);
  // "" defers to what the project last used.
  const [chosenKind, setChosenKind] = useState<"" | WorkspaceKind>("");
  const [naming, setNaming] = useState(false);
  const [baseRef, setBaseRef] = useState("");

  // Every choice here belongs to one git folder, so another folder starts
  // over rather than carrying a branch or a worktree that means nothing there.
  const scopeKey = JSON.stringify([project?.id, gitScope?.id]);
  const [choiceScope, setChoiceScope] = useState(scopeKey);
  if (choiceScope !== scopeKey) {
    setChoiceScope(scopeKey);
    setChoice(NO_CHOICE);
    setChosenKind("");
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
  const lastWorktree = remembered?.copy ?? project?.defaults.workspace === "managed";
  const kind: WorkspaceKind = !gitScope ? "main" : chosenKind || (lastWorktree ? "branch" : "main");
  // Branches already on disk are the useful bases: stacking on another worktree's
  // work is what the field is for.
  const baseChoices = Array.from(
    new Set(workspaces.map((w) => w.branch).filter((b): b is string => !!b)),
  ).filter((b) => b !== gitScope?.baseBranch);

  return {
    kind,
    label: labelFor(kind, choice, attachable),
    choice,
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
    sent: sentFor(kind, choice, baseRef),
    setChoice,
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
