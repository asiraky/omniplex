import type { DraftPreset } from "~/app/useThreadSelection";
import type { Folder, Project } from "~/protocol";

/**
 * How far through "New thread in…" the user is. A project alone means the
 * folder is still to pick; a project and a folder means the folder is git and
 * the workspace is still to pick. Anything that needs no further question
 * finishes on the pick instead of landing here.
 */
export interface NewThreadPicks {
  projectId?: string;
  folderId?: string;
}

export type NewThreadStep =
  | { kind: "project" }
  | { kind: "folder"; project: Project }
  | { kind: "workspace"; project: Project; folder: Folder };

export function currentStep(projects: Project[], picks: NewThreadPicks): NewThreadStep {
  const project = projects.find((p) => p.id === picks.projectId);
  if (!project) return { kind: "project" };
  if (picks.folderId === undefined) return { kind: "folder", project };
  const folder = project.folders.find((f) => f.id === picks.folderId);
  return folder ? { kind: "workspace", project, folder } : { kind: "folder", project };
}

/** Where a pick leads: the next question, or the finished preset. */
export type Next = { picks: NewThreadPicks } | { done: DraftPreset };

/** Picking a project. One folder is no question at all, so it is taken as picked. */
export function pickProject(project: Project): Next {
  if (project.folders.length > 1) return { picks: { projectId: project.id } };
  return pickFolder(project, project.folders[0]?.id ?? "");
}

/** Picking a folder, or "" for the whole project. Only a git folder asks about the workspace. */
export function pickFolder(project: Project, folderId: string): Next {
  const folder = project.folders.find((f) => f.id === folderId);
  if (folder?.git) return { picks: { projectId: project.id, folderId } };
  return { done: { projectId: project.id, folderId, kind: "main" } };
}

/**
 * One step back. From the workspace that is the folder, unless the project had
 * only the one and the folder was never asked; from the first step it leaves
 * the flow (null).
 */
export function stepBack(step: NewThreadStep): NewThreadPicks | null {
  switch (step.kind) {
    case "project":
      return null;
    case "folder":
      return {};
    case "workspace":
      return step.project.folders.length > 1 ? { projectId: step.project.id } : {};
  }
}

/** The default project first, the rest in the order they came. */
export function orderProjects(projects: Project[], defaultId: string | undefined): Project[] {
  const first = projects.find((p) => p.id === defaultId);
  return first ? [first, ...projects.filter((p) => p !== first)] : projects;
}
