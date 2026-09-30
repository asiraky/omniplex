import { PlusIcon } from "lucide-react";
import { useState } from "react";

import type { ConnectionStatus } from "~/client";
import type { Attachment } from "~/lib/attachments";
import { Composer } from "~/components/Composer";
import { Alert, AlertDescription } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import { saveLastProject } from "~/lib/lastProject";
import type { HarnessMeta, Project, UserConfig, Workspace } from "~/protocol";
import { AgentTools } from "./threadDraft/AgentTools";
import { GitChip } from "./threadDraft/GitChip";
import { InstanceAlerts } from "./threadDraft/InstanceAlerts";
import { FolderChip, ProjectChip } from "./threadDraft/ScopeChips";
import { useAgentChoice } from "./threadDraft/useAgentChoice";
import { useDraftScope } from "./threadDraft/useDraftScope";
import { useRememberedChoices } from "./threadDraft/useRememberedChoices";
import { useWorkspaceChoice, type IssueListing } from "./threadDraft/useWorkspaceChoice";

export type { IssueListing };

export interface NewThreadInput {
  projectId: string;
  /** The folder the thread works in; empty means the whole project. */
  folderId: string;
  harness: string;
  /** The provider instance to run under; empty means the harness's default. */
  instance: string;
  model: string;
  mode: string;
  effort: string;
  /** Empty agent fields are deliberate harness defaults, not omitted values. */
  agentSettingsExplicit: boolean;
  branch: string;
  workspace: string;
  workspacePath: string;
  /** The ref a new worktree branches from; empty defers to the folder default. */
  baseRef: string;
  /** The first message. The thread is created carrying it. */
  text: string;
}

/** Why sending is not possible yet, or "" when it is. */
function blockerFor({
  status,
  project,
  ready,
  missingCopy,
  loadingSpaces,
  starting,
}: {
  status: ConnectionStatus;
  project: Project | undefined;
  ready: boolean;
  missingCopy: boolean;
  loadingSpaces: boolean;
  starting: boolean;
}) {
  if (status !== "online") return "Reconnecting…";
  if (!project) return "Add a project first";
  if (!ready) return "Sign in to a model to start";
  if (missingCopy) return "Pick a copy to continue on";
  // The busy warning is made of this list, so wait for it.
  if (loadingSpaces) return "Loading copies…";
  if (starting) return "Starting…";
  return "";
}

/**
 * A thread before it exists: the composer, with the choices that shape the
 * thread as chips above it. Every chip starts on what was last used in the
 * project, so the usual case is typing and sending. Sending creates the thread
 * with the message already in it.
 */
export function ThreadDraft({
  projects,
  activeProjectId,
  harnesses,
  userConfig,
  status,
  draft,
  onDraftChange,
  onStart,
  attachments,
  onAttachFiles,
  onRemoveAttachment,
  onListWorkspaces,
  onListIssues,
  onAddProject,
  onSettings,
  onRecheck,
  onLogin,
  onManageProviders,
}: {
  projects: Project[];
  activeProjectId?: string;
  harnesses: HarnessMeta[];
  userConfig: UserConfig | null;
  status: ConnectionStatus;
  draft: string;
  onDraftChange: (text: string) => void;
  /** `schedule` starts the thread empty and opens a schedule for the message. */
  onStart: (input: NewThreadInput, schedule?: boolean) => Promise<void>;
  /** Files for the first message, held until the thread exists. */
  attachments?: Attachment[];
  onAttachFiles?: (files: File[]) => void;
  onRemoveAttachment?: (key: string) => void;
  onListWorkspaces: (projectId: string, folderId: string) => Promise<Workspace[]>;
  /** Separate from the workspaces so `gh` being slow cannot hold anything up. */
  onListIssues: (projectId: string, folderId: string) => Promise<IssueListing>;
  onAddProject: () => void;
  onSettings: (project: Project) => void;
  onRecheck: () => void;
  /** Open the harness's own sign-in for one instance; absent when the server cannot run one. */
  onLogin?: (instanceId: string) => void;
  /** Open the providers screen, for when signing in is not the fix. */
  onManageProviders?: () => void;
}) {
  const remembered = useRememberedChoices();
  const where = useDraftScope(projects, activeProjectId, remembered.preferences);
  const { project, scope, gitScope } = where;
  const agent = useAgentChoice(
    { project, remembered: where.remembered, harnesses, userConfig },
    remembered,
  );
  const git = useWorkspaceChoice({
    project,
    gitScope,
    remembered: where.remembered,
    onListWorkspaces,
    onListIssues,
  });
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const blocker = blockerFor({
    status,
    project,
    ready: agent.instance?.availability?.state === "ready",
    missingCopy: git.kind === "attach" && !git.choice.attachPath,
    loadingSpaces: git.loadingSpaces,
    starting,
  });

  const start = async (text: string, schedule = false) => {
    if (!project || blocker) return;
    setStarting(true);
    setError(null);
    try {
      await onStart(
        {
          projectId: project.id,
          folderId: scope?.id ?? "",
          harness: agent.harnessId,
          instance: agent.instance?.id ?? "",
          model: agent.effectiveModel,
          mode: agent.mode,
          effort: agent.effort,
          agentSettingsExplicit: true,
          ...git.sent,
          text,
        },
        schedule,
      );
      saveLastProject(project.id);
      remembered.recordStart(project.id, {
        harness: agent.harnessId,
        folderId: scope?.id ?? "",
        copy: gitScope ? git.kind !== "main" : where.remembered?.copy,
      });
    } catch (e) {
      // The composer cleared itself on send; the message goes back.
      onDraftChange(text);
      setError(e instanceof Error ? e.message : String(e));
      setStarting(false);
    }
  };

  if (projects.length === 0) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-4 px-6 pb-16 text-center">
        <p className="text-muted-foreground max-w-xs text-[13px]">
          Add a project once, then start every thread from here.
        </p>
        <Button onClick={onAddProject}>
          <PlusIcon />
          New project
        </Button>
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex flex-1 flex-col items-center justify-center gap-1.5 px-6 pb-6 text-center">
        <p className="text-[15px] font-medium">What are we working on?</p>
        <p className="text-muted-foreground max-w-sm text-[13px] leading-relaxed">
          Sending starts the thread. Omniplex prepares the workspace, then hands the agent your
          message.
        </p>
      </div>

      <div className="mx-auto w-full max-w-3xl space-y-2 px-4 md:px-5">
        <InstanceAlerts
          instances={agent.instances}
          onLogin={onLogin}
          onRecheck={onRecheck}
          onManageProviders={onManageProviders}
        />

        {error && (
          <Alert variant="destructive">
            <AlertDescription className="text-[12px] break-words">{error}</AlertDescription>
          </Alert>
        )}
      </div>

      {/* A block wrapper: the composer centres itself with auto margins,
          which in this flex column would shrink it to its content. */}
      <div>
        <Composer
          key="new-thread"
          tools={
            <AgentTools
              harness={agent.selected}
              modes={agent.modes}
              modeId={agent.displayModeId}
              supports1m={agent.supports1m}
              want1m={agent.want1m}
              onPickMode={(mode) => agent.remember({ mode })}
              onToggle1m={() => agent.remember({ want1m: !agent.want1m })}
            />
          }
          footer={
            // Where the thread runs, under the box: quiet, since it is
            // usually right already.
            <div
              role="group"
              aria-label="Thread options"
              className="scroll-thin flex gap-0.5 overflow-x-auto py-0.5"
            >
              <ProjectChip
                projects={projects}
                project={project}
                onPick={where.pickProject}
                onSettings={onSettings}
                onAddProject={onAddProject}
              />
              {where.folders.length > 1 && (
                <FolderChip folders={where.folders} scope={scope} onPick={where.pickFolder} />
              )}
              {gitScope && <GitChip folder={gitScope} git={git} userConfig={userConfig} />}
            </div>
          }
          draft={draft}
          onDraftChange={onDraftChange}
          disabled={false}
          sendDisabled={!!blocker}
          disabledPlaceholder={blocker}
          busy={false}
          onSend={(text) => void start(text)}
          onSchedule={() => void start(draft, true)}
          attachments={attachments}
          onAttachFiles={onAttachFiles}
          onRemoveAttachment={onRemoveAttachment}
          onCancel={() => {}}
          harnesses={harnesses}
          anyHarness
          harness={agent.harnessId}
          instance={agent.instance?.id ?? ""}
          model={agent.model}
          effort={agent.effort}
          onSwitchModel={agent.switchModel}
          onSwitchEffort={(effort) => agent.remember({ effort })}
          onPickInstance={agent.pickInstance}
          onSwitchAccount={agent.switchAccount}
        />
      </div>
    </div>
  );
}
