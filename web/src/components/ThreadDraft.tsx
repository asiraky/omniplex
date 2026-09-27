import {
  ChevronDownIcon,
  FolderIcon,
  GitBranchIcon,
  LayersIcon,
  LogInIcon,
  PlusIcon,
  RefreshCwIcon,
  SettingsIcon,
  ShieldIcon,
} from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";

import type { ConnectionStatus } from "~/client";
import { Composer } from "~/components/Composer";
import type { ModelSelection } from "~/components/ModelPicker";
import { Alert, AlertDescription } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Label } from "~/components/ui/label";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "~/components/ui/popover";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import { initialProject, saveLastProject } from "~/lib/lastProject";
import { defaultModel, pickerInstances, resolveInstance } from "~/lib/models";
import {
  loadThreadPrefs,
  saveThreadPrefs,
  type HarnessPrefs,
} from "~/lib/threadPrefs";
import { cn } from "~/lib/utils";
import type {
  HarnessMeta,
  Issue,
  Project,
  UserConfig,
  Workspace,
} from "~/protocol";
import { WorkspacePicker, type WorkspaceChoice } from "./WorkspacePicker";

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

export interface IssueListing {
  issues: Issue[];
  issuesError: string;
}

/** Work in the folder, on a new copy of it, or on a copy that already exists. */
type WorkspaceKind = "main" | "branch" | "attach";

// The Base dropdown's "use the folder default" option. A Radix Select item
// cannot carry an empty value, and the space makes it an impossible branch name.
const BASE_DEFAULT = "folder default";

const NO_PREFS: HarnessPrefs = {
  instance: "",
  model: "",
  mode: "",
  effort: "",
  want1m: false,
};

function folderName(path: string) {
  return path.replace(/\/+$/, "").split("/").pop() || path;
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
  onStart: (input: NewThreadInput) => Promise<void>;
  onListWorkspaces: (
    projectId: string,
    folderId: string,
  ) => Promise<Workspace[]>;
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
  const [projectId, setProjectId] = useState(() =>
    initialProject(projects, activeProjectId),
  );
  const [preferences, setPreferences] = useState(loadThreadPrefs);
  const [choice, setChoice] = useState<WorkspaceChoice>({
    branch: "",
    attachPath: "",
  });
  // "" defers to what the project last used.
  const [chosenKind, setChosenKind] = useState<"" | WorkspaceKind>("");
  // null defers to the folder the project last used; "" is the whole project.
  const [folderId, setFolderId] = useState<string | null>(null);
  const [naming, setNaming] = useState(false);
  const [baseRef, setBaseRef] = useState("");
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [issues, setIssues] = useState<IssueListing>({
    issues: [],
    issuesError: "",
  });
  const [loadingSpaces, setLoadingSpaces] = useState(false);
  const [loadingIssues, setLoadingIssues] = useState(false);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const project = projects.find((p) => p.id === projectId) ?? projects[0];
  const remembered = preferences[project?.id ?? ""];
  const folders = project?.folders ?? [];
  const wantedFolder = folderId ?? remembered?.folderId ?? "";
  // One folder is the scope with nothing to choose. With several, none chosen
  // is the whole project, which asks no git questions.
  const scope =
    folders.length === 1
      ? folders[0]
      : folders.find((f) => f.id === wantedFolder);
  const gitScope = scope?.git ? scope : undefined;

  const instances = pickerInstances(harnesses);
  const fallbackHarness =
    (harnesses.some((h) => h.id === remembered?.harness)
      ? remembered?.harness
      : "") ||
    project?.defaults.harness ||
    harnesses.find((h) => h.availability.state === "ready")?.id ||
    harnesses[0]?.id ||
    "";
  const instance = resolveInstance(
    instances,
    remembered?.byHarness[fallbackHarness]?.instance ?? "",
    fallbackHarness,
  );
  const harnessId = instance?.driver ?? fallbackHarness;
  const selected = harnesses.find((h) => h.id === harnessId);
  const chosen = remembered?.byHarness[harnessId];
  const want1m = chosen?.want1m ?? false;
  const harnessDefaults = project?.defaults.harnesses?.[harnessId];
  // A model the account no longer offers is not sent: the harness's own
  // default is a better answer than a name it has stopped serving.
  const preferred = chosen?.model ?? harnessDefaults?.model ?? "";
  const model = instance?.models.some((m) => m.id === preferred)
    ? preferred
    : (defaultModel(instance)?.id ?? "");
  const modelMeta = instance?.models.find((m) => m.id === model);
  // The adapter marks the models it saw a "[1m]" alias for; that tag on the
  // model id is what it turns into the context-window setting at start.
  const supports1m = modelMeta?.supports1m ?? false;
  const effectiveModel = supports1m && want1m ? `${model}[1m]` : model;
  const efforts = modelMeta?.efforts ?? [];
  const preferredEffort = chosen?.effort ?? harnessDefaults?.effort ?? "";
  const effort = efforts.includes(preferredEffort) ? preferredEffort : "";
  // Only an expressed preference is sent; otherwise the harness's own
  // configured default wins.
  const modes = selected?.permissionModes ?? [];
  const preferredMode = chosen?.mode ?? harnessDefaults?.mode ?? "";
  const mode = modes.some((m) => m.id === preferredMode) ? preferredMode : "";
  const displayModeId =
    mode || (modes.find((m) => m.default)?.id ?? modes[0]?.id ?? "");
  const modeMeta = modes.find((m) => m.id === displayModeId);

  // The folder itself is its own choice, so it is not offered again as a copy.
  const attachable = workspaces.filter((w) => !w.isRoot);
  const lastCopy =
    remembered?.copy ?? project?.defaults.workspace === "managed";
  const kind: WorkspaceKind = !gitScope
    ? "main"
    : chosenKind || (lastCopy ? "branch" : "main");
  const branch = kind === "branch" ? choice.branch.trim() : "";
  const workspace =
    kind === "main" ? "local" : kind === "attach" ? "" : "managed";
  const workspacePath = kind === "attach" ? choice.attachPath : "";
  // A base only means anything where Omniplex is the one creating the branch.
  const sentBase = kind === "branch" ? baseRef.trim() : "";
  // Branches already on disk are the useful bases: stacking on another copy's
  // work is what the field is for.
  const baseChoices = Array.from(
    new Set(workspaces.map((w) => w.branch).filter((b): b is string => !!b)),
  ).filter((b) => b !== gitScope?.baseBranch);

  const blocker =
    status !== "online"
      ? "Reconnecting…"
      : !project
        ? "Add a project first"
        : instance?.availability?.state !== "ready"
          ? "Sign in to a model to start"
          : kind === "attach" && !choice.attachPath
            ? "Pick a copy to continue on"
            : // The busy warning is made of this list, so wait for it.
              loadingSpaces
              ? "Loading copies…"
              : starting
                ? "Starting…"
                : "";

  // Catalogue validation affects what we send, not the preference we keep.
  const currentPrefs: HarnessPrefs = {
    instance: instance?.id ?? "",
    model: chosen?.model ?? model,
    mode: preferredMode,
    effort: preferredEffort,
    want1m,
  };
  // A patch over the latest saved choices, not this render's: one pick can
  // change the model and then the effort, and the second must not undo the
  // first.
  const remember = (harness: string, patch: Partial<HarnessPrefs>) => {
    if (!project) return;
    const pid = project.id;
    const base = harness === harnessId ? currentPrefs : undefined;
    setPreferences((prev) => {
      const saved = prev[pid];
      const old = saved?.byHarness[harness] ?? base;
      const next = {
        ...prev,
        [pid]: {
          ...saved,
          harness,
          byHarness: {
            ...saved?.byHarness,
            [harness]: { ...(old ?? NO_PREFS), ...patch },
          },
        },
      };
      saveThreadPrefs(next);
      return next;
    });
  };
  const selectModel = (next: ModelSelection) => {
    const previous = remembered?.byHarness[next.harness];
    const seed = project?.defaults.harnesses?.[next.harness];
    remember(next.harness, {
      instance: next.instance,
      model: next.model,
      mode: previous?.mode ?? seed?.mode ?? "",
      effort: previous?.effort ?? seed?.effort ?? "",
      want1m: previous?.want1m ?? false,
    });
  };

  const start = async (text: string) => {
    if (!project || blocker) return;
    setStarting(true);
    setError(null);
    try {
      await onStart({
        projectId: project.id,
        folderId: scope?.id ?? "",
        harness: harnessId,
        instance: instance?.id ?? "",
        model: effectiveModel,
        mode,
        effort,
        agentSettingsExplicit: true,
        branch,
        workspace,
        workspacePath,
        baseRef: sentBase,
        text,
      });
      // Remembered on a thread that actually started, not on every pick.
      saveLastProject(project.id);
      const next = {
        ...preferences,
        [project.id]: {
          harness: harnessId,
          byHarness: remembered?.byHarness ?? {},
          folderId: scope?.id ?? "",
          ...(gitScope
            ? { copy: kind !== "main" }
            : { copy: remembered?.copy }),
        },
      };
      saveThreadPrefs(next);
    } catch (e) {
      // The composer cleared itself on send; the message goes back.
      onDraftChange(text);
      setError(e instanceof Error ? e.message : String(e));
      setStarting(false);
    }
  };

  // The draft can mount before the project list has landed.
  useEffect(() => {
    if (!projectId && projects.length > 0)
      setProjectId(initialProject(projects, activeProjectId));
  }, [projectId, projects]);

  // A folder id means nothing in another project.
  useEffect(() => {
    setFolderId(null);
  }, [project?.id]);

  // Copies and issues belong to a git folder, re-read whenever the scope
  // changes so a stale list cannot offer a copy that has since gone.
  useEffect(() => {
    setLoadingSpaces(false);
    setWorkspaces([]);
    setChoice({ branch: "", attachPath: "" });
    setChosenKind("");
    setBaseRef("");
    setNaming(false);
    if (!project || !gitScope) return;
    let live = true;
    setLoadingSpaces(true);
    onListWorkspaces(project.id, gitScope.id)
      .then((r) => live && setWorkspaces(r))
      .catch(() => live && setWorkspaces([]))
      .finally(() => live && setLoadingSpaces(false));
    return () => {
      live = false;
    };
  }, [project?.id, gitScope?.id, onListWorkspaces]);

  // `gh` may take seconds to answer and nothing here waits on it.
  useEffect(() => {
    setLoadingIssues(false);
    setIssues({ issues: [], issuesError: "" });
    if (!project || !gitScope) return;
    let live = true;
    setLoadingIssues(true);
    onListIssues(project.id, gitScope.id)
      .then((r) => live && setIssues(r))
      .catch(
        (e) =>
          live &&
          setIssues({
            issues: [],
            issuesError: e instanceof Error ? e.message : String(e),
          }),
      )
      .finally(() => live && setLoadingIssues(false));
    return () => {
      live = false;
    };
  }, [project?.id, gitScope?.id, onListIssues]);

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

  const attached = attachable.find((w) => w.path === choice.attachPath);
  const gitLabel =
    kind === "main"
      ? "In the folder"
      : kind === "attach"
        ? attached
          ? `Copy: ${attached.branch || folderName(attached.path)}`
          : "Existing copy"
        : branch
          ? `New copy: ${branch}`
          : "New copy";

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex flex-1 flex-col items-center justify-center gap-1.5 px-6 pb-6 text-center">
        <p className="text-[15px] font-medium">What are we working on?</p>
        <p className="text-muted-foreground max-w-sm text-[13px] leading-relaxed">
          Sending starts the thread. Omniplex prepares the workspace, then hands
          the agent your message.
        </p>
      </div>

      <div className="mx-auto w-full max-w-3xl space-y-2 px-4 md:px-5">
        {/* Every account that cannot start, not just the chosen one: the
            picker falls back to whatever is ready, so a signed-out account
            would otherwise vanish with no word of why. */}
        {instances
          .filter((i) => i.enabled && i.availability?.state !== "ready")
          .map((i) => (
            <Alert key={i.id}>
              <AlertDescription>
                <span>
                  {instances.length > 1 && (
                    <span className="font-medium">{i.name}: </span>
                  )}
                  {i.availability?.reason}
                </span>
                <div className="mt-2 flex flex-wrap gap-2">
                  {onLogin &&
                    i.availability?.remedy?.some(
                      (r) => r.action === "login",
                    ) && (
                      <Button size="sm" onClick={() => onLogin(i.id)}>
                        <LogInIcon />
                        Sign in
                      </Button>
                    )}
                  <Button variant="outline" size="sm" onClick={onRecheck}>
                    <RefreshCwIcon />
                    Check again
                  </Button>
                  {onManageProviders && (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={onManageProviders}
                    >
                      Providers…
                    </Button>
                  )}
                </div>
              </AlertDescription>
            </Alert>
          ))}

        {error && (
          <Alert variant="destructive">
            <AlertDescription className="text-[12px] break-words">
              {error}
            </AlertDescription>
          </Alert>
        )}

        <div
          className="flex flex-wrap gap-1.5"
          role="group"
          aria-label="Thread options"
        >
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Chip label="Project" icon={<FolderIcon />}>
                {project?.name}
              </Chip>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="min-w-52">
              <DropdownMenuRadioGroup
                value={project?.id}
                onValueChange={setProjectId}
              >
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

          {folders.length > 1 && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Chip label="Scope" icon={<LayersIcon />}>
                  {scope ? folderName(scope.path) : "Everything"}
                </Chip>
              </DropdownMenuTrigger>
              <DropdownMenuContent
                align="start"
                className="max-w-[min(22rem,calc(100vw-2rem))]"
              >
                <DropdownMenuRadioGroup
                  value={scope?.id ?? ""}
                  onValueChange={(v) => setFolderId(v)}
                >
                  <DropdownMenuRadioItem value="">
                    <Described
                      title="Everything"
                      hint="Every folder, worked on directly"
                    />
                  </DropdownMenuRadioItem>
                  {folders.map((f) => (
                    <DropdownMenuRadioItem key={f.id} value={f.id}>
                      <Described title={folderName(f.path)} hint={f.path} />
                    </DropdownMenuRadioItem>
                  ))}
                </DropdownMenuRadioGroup>
              </DropdownMenuContent>
            </DropdownMenu>
          )}

          {gitScope && (
            <Popover>
              <PopoverTrigger asChild>
                <Chip label="Git" icon={<GitBranchIcon />}>
                  {gitLabel}
                </Chip>
              </PopoverTrigger>
              <PopoverContent
                align="start"
                className="w-[min(22rem,calc(100vw-2rem))] space-y-2"
              >
                <div
                  role="radiogroup"
                  aria-label="Git"
                  className="flex flex-col gap-1.5"
                >
                  {(
                    [
                      {
                        id: "main",
                        label: "Work in the folder",
                        hint: gitScope.path,
                      },
                      {
                        id: "branch",
                        label: "Work on a copy",
                        hint: "A checkout of its own on a new branch. The folder stays as it is.",
                      },
                    ] as const
                  ).map((k) => {
                    const picked =
                      k.id === "main" ? kind === "main" : kind !== "main";
                    return (
                      <button
                        key={k.id}
                        type="button"
                        role="radio"
                        aria-checked={picked}
                        onClick={() => {
                          if (picked) return;
                          setChosenKind(k.id);
                          setChoice({ branch: "", attachPath: "" });
                        }}
                        className={cn(
                          "focus-visible:ring-ring flex min-h-11 flex-col justify-center gap-0.5 rounded-lg border px-3 py-2 text-left transition-colors outline-none focus-visible:ring-2",
                          picked
                            ? "border-primary/60 bg-primary/10"
                            : "hover:bg-accent/50",
                        )}
                      >
                        <span className="text-[13px] leading-tight">
                          {k.label}
                        </span>
                        <span className="text-muted-foreground truncate text-[11px] leading-tight">
                          {k.hint}
                        </span>
                      </button>
                    );
                  })}
                </div>

                {kind === "branch" && (
                  <div className="flex flex-wrap gap-x-3">
                    <Button
                      type="button"
                      variant="link"
                      size="sm"
                      aria-expanded={naming}
                      className="h-8 px-0 text-[12px]"
                      onClick={() => setNaming(!naming)}
                    >
                      {naming
                        ? "Let Omniplex name the branch"
                        : "Name the branch"}
                    </Button>
                    {attachable.length > 0 && (
                      <Button
                        type="button"
                        variant="link"
                        size="sm"
                        className="h-8 px-0 text-[12px]"
                        onClick={() => {
                          setChosenKind("attach");
                          setChoice({ branch: "", attachPath: "" });
                        }}
                      >
                        Continue on an existing copy
                      </Button>
                    )}
                  </div>
                )}

                {kind === "branch" && naming && (
                  <>
                    <div className="space-y-1.5">
                      <Label htmlFor="new-thread-workspace">Branch</Label>
                      <WorkspacePicker
                        id="new-thread-workspace"
                        mode="create"
                        value={choice}
                        onChange={setChoice}
                        workspaces={attachable}
                        issues={issues.issues}
                        issuesError={issues.issuesError}
                        userConfig={userConfig}
                        loading={loadingIssues}
                        placeholder="issue/482-fix-login"
                      />
                    </div>
                    <div className="space-y-1.5 pt-1">
                      <Label htmlFor="new-thread-base">Base</Label>
                      <Select
                        value={baseRef || BASE_DEFAULT}
                        onValueChange={(v) =>
                          setBaseRef(v === BASE_DEFAULT ? "" : v)
                        }
                      >
                        <SelectTrigger id="new-thread-base" className="w-full">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value={BASE_DEFAULT}>
                            Folder default
                            {gitScope.baseBranch
                              ? ` (${gitScope.baseBranch})`
                              : ""}
                          </SelectItem>
                          {baseChoices.map((b) => (
                            <SelectItem key={b} value={b}>
                              {b}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                  </>
                )}

                {kind === "attach" && (
                  <div className="space-y-1.5">
                    <div className="flex items-center justify-between gap-2">
                      <Label htmlFor="new-thread-attach">Existing copy</Label>
                      <Button
                        type="button"
                        variant="link"
                        size="sm"
                        className="h-8 px-0 text-[12px]"
                        onClick={() => {
                          setChosenKind("branch");
                          setChoice({ branch: "", attachPath: "" });
                        }}
                      >
                        Start a new copy instead
                      </Button>
                    </div>
                    <WorkspacePicker
                      id="new-thread-attach"
                      mode="attach"
                      value={choice}
                      onChange={setChoice}
                      workspaces={attachable}
                      issues={issues.issues}
                      issuesError={issues.issuesError}
                      userConfig={userConfig}
                      loading={loadingSpaces}
                      placeholder="Search copies"
                    />
                  </div>
                )}
              </PopoverContent>
            </Popover>
          )}

          {modes.length > 0 && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Chip label="Permissions" icon={<ShieldIcon />}>
                  {modeMeta?.label}
                </Chip>
              </DropdownMenuTrigger>
              <DropdownMenuContent
                align="start"
                className="max-w-[min(22rem,calc(100vw-2rem))]"
              >
                <DropdownMenuRadioGroup
                  value={displayModeId}
                  onValueChange={(mode) => remember(harnessId, { mode })}
                >
                  {modes.map((m) => (
                    <DropdownMenuRadioItem key={m.id} value={m.id}>
                      <Described title={m.label} hint={m.description} />
                    </DropdownMenuRadioItem>
                  ))}
                </DropdownMenuRadioGroup>
              </DropdownMenuContent>
            </DropdownMenu>
          )}

          {supports1m && (
            <Button
              type="button"
              variant="outline"
              size="sm"
              aria-pressed={want1m}
              title={want1m ? "Larger window, higher cost" : "Standard window"}
              onClick={() => remember(harnessId, { want1m: !want1m })}
              className={cn(chipClass, want1m && "bg-accent")}
            >
              1M context
            </Button>
          )}
        </div>
      </div>

      {/* A block wrapper: the composer centres itself with auto margins,
          which in this flex column would shrink it to its content. */}
      <div>
        <Composer
          key="new-thread"
          draft={draft}
          onDraftChange={onDraftChange}
          disabled={false}
          sendDisabled={!!blocker}
          disabledPlaceholder={blocker}
          busy={false}
          onSend={(text) => void start(text)}
          onCancel={() => {}}
          harnesses={harnesses}
          anyHarness
          harness={harnessId}
          instance={instance?.id ?? ""}
          model={model}
          effort={effort}
          onSwitchModel={(next) =>
            selectModel({
              harness: harnessId,
              instance: instance?.id ?? "",
              model: next,
            })
          }
          onSwitchEffort={(effort) => remember(harnessId, { effort })}
          onPickInstance={(target) => {
            if (target.id === instance?.id) return;
            const previous = remembered?.byHarness[target.driver];
            const wanted =
              previous?.model ??
              project?.defaults.harnesses?.[target.driver]?.model;
            const restored =
              target.models.find((m) => m.id === wanted) ??
              defaultModel(target);
            selectModel({
              harness: target.driver,
              instance: target.id,
              model: restored?.id ?? "",
            });
          }}
          onSwitchAccount={(id, next) => {
            const target = instances.find((i) => i.id === id);
            if (target)
              selectModel({
                harness: target.driver,
                instance: id,
                model: next,
              });
          }}
        />
      </div>
    </div>
  );
}

const chipClass =
  "h-9 max-w-full gap-1.5 rounded-full px-3 text-[12px] font-normal md:h-7 [&_svg]:size-3.5";

function Chip({
  label,
  icon,
  children,
  ...props
}: {
  label: string;
  icon: ReactNode;
  children: ReactNode;
} & React.ComponentProps<"button">) {
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      aria-label={`${label}: ${typeof children === "string" ? children : ""}`.trim()}
      className={chipClass}
      {...props}
    >
      <span className="text-muted-foreground">{icon}</span>
      <span className="truncate">{children}</span>
      <ChevronDownIcon className="text-muted-foreground" />
    </Button>
  );
}

function Described({ title, hint }: { title: string; hint?: string }) {
  return (
    <span className="flex min-w-0 flex-col">
      <span className="text-[13px]">{title}</span>
      {hint && (
        <span className="text-muted-foreground truncate text-[11px]">
          {hint}
        </span>
      )}
    </span>
  );
}
