import { FolderIcon, LayersIcon, PlusIcon, SettingsIcon } from "lucide-react";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import type { Folder, Project } from "~/protocol";
import { Chip, Described } from "./parts";
import { folderName } from "./shared";

/** Which project the thread belongs to, with the way to add or set one up. */
export function ProjectChip({
  projects,
  project,
  onPick,
  onSettings,
  onAddProject,
}: {
  projects: Project[];
  project: Project | undefined;
  onPick: (projectId: string) => void;
  onSettings: (project: Project) => void;
  onAddProject: () => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Chip label="Project" icon={<FolderIcon />}>
          {project?.name}
        </Chip>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-52">
        <DropdownMenuRadioGroup value={project?.id} onValueChange={onPick}>
          {projects.map((p) => (
            <DropdownMenuRadioItem key={p.id} value={p.id}>
              {p.name}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => project && onSettings(project)}>
          <SettingsIcon /> Project settings
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={onAddProject}>
          <PlusIcon /> New project…
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** One folder of a several-folder project, or all of them. */
export function FolderChip({
  folders,
  scope,
  onPick,
}: {
  folders: Folder[];
  scope: Folder | undefined;
  onPick: (folderId: string) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Chip label="Scope" icon={<LayersIcon />}>
          {scope ? folderName(scope.path) : "Everything"}
        </Chip>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-w-[min(22rem,calc(100vw-2rem))]">
        <DropdownMenuRadioGroup value={scope?.id ?? ""} onValueChange={onPick}>
          <DropdownMenuRadioItem value="">
            <Described title="Everything" hint="Every folder, worked on directly" />
          </DropdownMenuRadioItem>
          {folders.map((f) => (
            <DropdownMenuRadioItem key={f.id} value={f.id}>
              <Described title={folderName(f.path)} hint={f.path} />
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
