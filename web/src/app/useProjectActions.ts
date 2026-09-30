import { useCallback, useMemo } from "react";

import type { NewProjectRequest } from "~/components/NewProject";
import type { AddFolderRequest } from "~/components/ProjectSettings";
import type { Folder, GitHubRepo, Project, ProjectDefaults, Workspace } from "~/protocol";

import type { Wire } from "./useWire";

export type ProjectActions = ReturnType<typeof useProjectActions>;

/**
 * Creating, editing and forgetting projects and their folders, and the
 * lookups the new-thread form makes against a folder.
 */
export function useProjectActions(wire: Wire) {
  const { clientRef, setProjects, threads } = wire;
  const putProject = useCallback(
    (project: Project) => {
      setProjects((p) => [project, ...p.filter((x) => x.id !== project.id)]);
      return project;
    },
    [setProjects],
  );
  const createProject = useCallback(
    async (req: NewProjectRequest) =>
      putProject((await clientRef.current!.command("create_project", req)).project as Project),
    [clientRef, putProject],
  );
  const addFolder = useCallback(
    async (projectId: string, req: AddFolderRequest) =>
      putProject(
        (await clientRef.current!.command("add_folder", { projectId, ...req })).project as Project,
      ),
    [clientRef, putProject],
  );
  const removeFolder = useCallback(
    async (projectId: string, folderId: string) =>
      putProject(
        (await clientRef.current!.command("remove_folder", { projectId, folderId }))
          .project as Project,
      ),
    [clientRef, putProject],
  );
  const listRepos = useCallback(
    async () => (await clientRef.current!.command("list_github_repos", {})).repos as GitHubRepo[],
    [clientRef],
  );
  // A project's own settings and each changed folder's are separate saves; the
  // last answer carries every one of them.
  const saveProject = useCallback(
    async (projectId: string, name: string, defaults: ProjectDefaults, folders: Folder[]) => {
      let res = await clientRef.current!.command("save_project", { projectId, name, defaults });
      for (const folder of folders) {
        // react-doctor-disable-next-line react-doctor/async-await-in-loop -- sequential on purpose: the server runs each command in its own goroutine and answers each with the project as of that save, so only a chain makes the last answer carry every save, and a failure stops the rest
        res = await clientRef.current!.command("save_folder", { projectId, folder });
      }
      setProjects((p) => p.map((x) => (x.id === projectId ? res.project : x)));
    },
    [clientRef, setProjects],
  );
  // Forgetting a project touches nothing on disk, so the only thing to undo
  // locally is the list. The server broadcasts the new one to every other
  // device anyway; dropping it here just means this one does not wait for it.
  const deleteProject = useCallback(
    async (projectId: string) => {
      await clientRef.current!.command("delete_project", { projectId });
      setProjects((p) => p.filter((x) => x.id !== projectId));
    },
    [clientRef, setProjects],
  );

  const listWorkspaces = useCallback(
    async (projectId: string, folderId: string) => {
      const res = await clientRef.current!.command("list_workspaces", { projectId, folderId });
      return (res.workspaces ?? []) as Workspace[];
    },
    [clientRef],
  );
  // Its own request: `gh` can take seconds, and nothing that shapes a choice
  // should be waiting behind it.
  const listIssues = useCallback(
    async (projectId: string, folderId: string) => {
      const res = await clientRef.current!.command("list_issues", { projectId, folderId });
      return { issues: res.issues ?? [], issuesError: res.issuesError ?? "" };
    },
    [clientRef],
  );

  // Only a project with no threads can be deleted; settings says so up front.
  const threadCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const t of threads) if (t.projectId) counts[t.projectId] = (counts[t.projectId] ?? 0) + 1;
    return counts;
  }, [threads]);

  return {
    createProject,
    addFolder,
    removeFolder,
    listRepos,
    saveProject,
    deleteProject,
    listWorkspaces,
    listIssues,
    threadCounts,
  };
}
