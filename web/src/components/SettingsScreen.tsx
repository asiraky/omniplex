import {
  ChevronRightIcon,
  FolderIcon,
  KeyRoundIcon,
  PlusIcon,
  SlidersHorizontalIcon,
} from "lucide-react";
import { useState, type ReactNode } from "react";

import type { AuthWires } from "~/components/AuthFlowDialog";
import { GeneralSettings } from "~/components/GeneralSettings";
import { ProjectSettings, type AddFolderRequest } from "~/components/ProjectSettings";
import ProvidersSettings from "~/components/ProvidersSettings";
import { SettingsPane } from "~/components/SettingsPane";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "~/components/ui/dialog";
import { useIsDesktop } from "~/useMediaQuery";
import { cn } from "~/lib/utils";
import type {
  Folder,
  GitHubRepo,
  HarnessMeta,
  Project,
  ProjectDefaults,
  UserConfig,
} from "~/protocol";

export type SettingsSection =
  { kind: "general" } | { kind: "providers" } | { kind: "project"; id: string };

const GENERAL: SettingsSection = { kind: "general" };

const same = (a: SettingsSection | null, b: SettingsSection) =>
  !!a && a.kind === b.kind && (a.kind !== "project" || (b.kind === "project" && a.id === b.id));

/**
 * Every setting in one place: yours, the accounts the agents sign in with, and
 * each project's. A list of sections beside the open one on a desktop; on a
 * phone the list first, then the section over it.
 */
export function SettingsScreen({
  at,
  projects,
  harnesses,
  userConfig,
  threadCounts,
  onSaveUserConfig,
  providers,
  project: projectActions,
  onAddProject,
  onClose,
}: {
  /** The section to open on. Absent opens the list on a phone, General on a desktop. */
  at?: SettingsSection;
  projects: Project[];
  harnesses: HarnessMeta[];
  /** Null until the server has answered; General waits for it. */
  userConfig: UserConfig | null;
  threadCounts: Record<string, number>;
  onSaveUserConfig: (cfg: UserConfig) => Promise<void>;
  providers: {
    wires: AuthWires;
    onOpenTerminal: (instanceId: string) => void;
    onRecheck: () => Promise<void> | void;
  };
  project: {
    onSave: (
      id: string,
      name: string,
      defaults: ProjectDefaults,
      folders: Folder[],
    ) => Promise<void>;
    onAddFolder: (projectId: string, req: AddFolderRequest) => Promise<Project>;
    onRemoveFolder: (projectId: string, folderId: string) => Promise<Project>;
    listRepos: () => Promise<GitHubRepo[]>;
    onDelete: (id: string) => Promise<void>;
  };
  onAddProject: () => void;
  onClose: () => void;
}) {
  const isDesktop = useIsDesktop();
  const [picked, setPicked] = useState<SettingsSection | null>(at ?? null);
  // A project deleted from under its own section leaves nothing to show.
  const valid =
    picked?.kind === "project" && !projects.some((p) => p.id === picked.id) ? null : picked;
  const section = valid ?? (isDesktop ? GENERAL : null);
  const back = isDesktop ? undefined : () => setPicked(null);

  let pane: ReactNode = null;
  if (section?.kind === "general") {
    pane = userConfig ? (
      <GeneralSettings
        userConfig={userConfig}
        harnesses={harnesses}
        onSave={onSaveUserConfig}
        onBack={back}
      />
    ) : (
      <SettingsPane title="General" onBack={back}>
        <p className="text-muted-foreground text-[13px]">Loading…</p>
      </SettingsPane>
    );
  } else if (section?.kind === "providers") {
    pane = <ProvidersSettings harnesses={harnesses} {...providers} onBack={back} />;
  } else if (section?.kind === "project") {
    const project = projects.find((p) => p.id === section.id)!;
    pane = (
      <ProjectSettings
        // A different project is a different form, not this one's edits.
        key={project.id}
        project={project}
        harnesses={harnesses}
        {...projectActions}
        threadCount={threadCounts[project.id] ?? 0}
        onDeleted={() => setPicked(null)}
        onBack={back}
      />
    );
  }

  const item = (target: SettingsSection, icon: ReactNode, label: string) => (
    <NavItem
      key={target.kind === "project" ? target.id : target.kind}
      icon={icon}
      label={label}
      current={isDesktop && same(section, target)}
      onClick={() => setPicked(target)}
    />
  );

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        fullscreenOnMobile
        className="flex gap-0 p-0 md:h-[min(90dvh,44rem)] md:max-w-3xl"
      >
        <DialogTitle className="sr-only">Settings</DialogTitle>
        <DialogDescription className="sr-only">
          Your defaults, the accounts agents sign in with, and each project's settings.
        </DialogDescription>

        {(isDesktop || !section) && (
          <nav
            aria-label="Settings sections"
            className={cn(
              "scroll-thin flex min-h-0 flex-col overflow-y-auto",
              isDesktop
                ? "bg-muted/40 w-56 shrink-0 rounded-l-lg border-r p-2"
                : "w-full px-3 pt-[calc(0.75rem+env(safe-area-inset-top))] pb-3",
            )}
          >
            <p
              aria-hidden
              className="px-2.5 pt-2 pb-3 text-lg font-semibold md:pt-1.5 md:pb-2 md:text-base"
            >
              Settings
            </p>
            <div className="flex flex-col gap-0.5">
              {item(GENERAL, <SlidersHorizontalIcon />, "General")}
              {item({ kind: "providers" }, <KeyRoundIcon />, "Providers")}
            </div>
            <p className="text-muted-foreground px-2.5 pt-4 pb-1 text-[11px] font-medium">
              Projects
            </p>
            <div className="flex flex-col gap-0.5">
              {projects.map((p) => item({ kind: "project", id: p.id }, <FolderIcon />, p.name))}
              <NavItem icon={<PlusIcon />} label="New project" onClick={onAddProject} muted />
            </div>
          </nav>
        )}

        {pane}
      </DialogContent>
    </Dialog>
  );
}

function NavItem({
  icon,
  label,
  current,
  muted,
  onClick,
}: {
  icon: ReactNode;
  label: string;
  current?: boolean;
  muted?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={current ? "page" : undefined}
      className={cn(
        "hover:bg-accent focus-visible:ring-ring flex min-h-11 w-full items-center gap-2.5 rounded-md px-2.5 text-left text-[14px] outline-none focus-visible:ring-2 md:min-h-8 md:text-[13px] [&_svg]:size-4 [&_svg]:shrink-0",
        current && "bg-accent text-accent-foreground font-medium",
        muted && "text-muted-foreground",
      )}
    >
      <span className="text-muted-foreground flex">{icon}</span>
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {!muted && <ChevronRightIcon className="text-muted-foreground md:hidden" />}
    </button>
  );
}
