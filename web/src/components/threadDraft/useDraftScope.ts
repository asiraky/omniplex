import { useState } from "react";

import { initialProject } from "~/lib/lastProject";
import type { ThreadPrefs } from "~/lib/threadPrefs";
import type { Project } from "~/protocol";

/**
 * Where the thread will work: the project, and within it one folder or the
 * whole project. Both start on what the project last used, unless the draft
 * was asked for in a given folder.
 */
export function useDraftScope(
  projects: Project[],
  activeProjectId: string | undefined,
  preferences: ThreadPrefs,
  /** A folder of `activeProjectId` to open on, over the remembered one. */
  initialFolderId?: string,
) {
  const [chosenId, setProjectId] = useState(() => initialProject(projects, activeProjectId));
  // The draft can mount before the project list has landed. Settled during
  // render rather than in an effect, so the first render with projects is
  // already on the right one, and so is everything this render hands on.
  const settled = !chosenId && projects.length > 0;
  const projectId = settled ? initialProject(projects, activeProjectId) : chosenId;
  if (settled) setProjectId(projectId);

  // A folder picked in this draft, with the project it belongs to: an id
  // means nothing in another project. "" is the whole project. A folder the
  // draft was asked for counts as picked in its project.
  const [picked, setPicked] = useState<{ projectId: string; folderId: string } | null>(() =>
    activeProjectId && initialFolderId !== undefined
      ? { projectId: activeProjectId, folderId: initialFolderId }
      : null,
  );

  const project = projects.find((p) => p.id === projectId) ?? projects[0];
  const remembered = preferences[project?.id ?? ""];
  const folders = project?.folders ?? [];
  // A pick in this project, else the folder the project last used.
  const folderId = picked && picked.projectId === project?.id ? picked.folderId : null;
  const wantedFolder = folderId ?? remembered?.folderId ?? "";
  // One folder is the scope with nothing to choose. With several, none chosen
  // is the whole project, which asks no git questions.
  const scope = folders.length === 1 ? folders[0] : folders.find((f) => f.id === wantedFolder);
  const gitScope = scope?.git ? scope : undefined;

  const pickFolder = (folderId: string) => {
    if (project) setPicked({ projectId: project.id, folderId });
  };

  return {
    project,
    remembered,
    folders,
    scope,
    gitScope,
    pickProject: setProjectId,
    pickFolder,
  };
}
