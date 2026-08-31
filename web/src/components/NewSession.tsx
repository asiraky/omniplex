import { LogInIcon, PlusIcon, RefreshCwIcon, SettingsIcon } from "lucide-react";
import { useEffect, useState } from "react";

import type { ConnectionStatus } from "~/client";
import { ModelPicker, type ModelSelection } from "~/components/ModelPicker";
import { Alert, AlertDescription } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Label } from "~/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import { initialProject, saveLastProject } from "~/lib/lastProject";
import { defaultModel, pickerInstances, resolveInstance } from "~/lib/models";
import { cn } from "~/lib/utils";
import type { HarnessMeta, Issue, Project, UserConfig, Workspace } from "~/protocol";
import { WorkspacePicker, type WorkspaceChoice } from "./WorkspacePicker";

export interface NewSessionInput {
  projectId: string;
  harness: string;
  /** The provider instance to run under; empty means the harness's default. */
  instance: string;
  model: string;
  mode: string;
  effort: string;
  branch: string;
  workspace: string;
  workspacePath: string;
  /** The ref a new worktree branches from; empty defers to the project default. */
  baseRef: string;
}

/**
 * The scenarios, as things to click. They were all reachable before — some of
 * them only by knowing that an empty field meant something, or that a dropdown
 * on a "Branch" label also attached — which is not the same as being offered.
 * The scratch case has since folded into "branch": leaving its name blank is
 * the same as naming nothing at all, so it no longer earns a tile of its own.
 */
type WorkspaceKind = "main" | "branch" | "attach";

// The Base dropdown's "use the project default" option. A Radix Select item
// cannot carry an empty value, so this stands in for the empty baseRef that
// defers to the project's own base branch. The space makes it an impossible
// Git branch name, so it can never collide with a real branch listed beside
// it and be mistaken for the default.
const BASE_DEFAULT = "project default";

const WORKSPACE_KINDS: { id: WorkspaceKind; label: string; hint: string }[] = [
  { id: "main", label: "Main checkout", hint: "Works in the project directory itself." },
  {
    id: "branch",
    label: "New worktree from issue or branch name",
    hint: "A fresh checkout on a branch you name.",
  },
  {
    id: "attach",
    label: "Attach to existing worktree",
    hint: "Run in a checkout that is already there.",
  },
];

export interface IssueListing {
  issues: Issue[];
  issuesError: string;
}

export function NewSession({
  projects,
  harnesses,
  userConfig,
  onCreate,
  onListWorkspaces,
  onListIssues,
  onAddProject,
  onSettings,
  onRecheck,
  onLogin,
  onClose,
  status,
}: {
  projects: Project[];
  harnesses: HarnessMeta[];
  userConfig: UserConfig | null;
  onCreate: (input: NewSessionInput) => Promise<void>;
  onListWorkspaces: (projectId: string) => Promise<Workspace[]>;
  /** Separate from the workspaces so `gh` being slow cannot delay the busy warning. */
  onListIssues: (projectId: string) => Promise<IssueListing>;
  onAddProject: () => void;
  onSettings: (project: Project) => void;
  onRecheck: () => void;
  /** Open the harness's own sign-in for one instance; absent when the server cannot run one. */
  onLogin?: (instanceId: string) => void;
  onClose: () => void;
  status: ConnectionStatus;
}) {
  // Opens on the project this browser last started a session from. Everything
  // else in this form stays project-derived: the harness, model and workspace
  // defaults are the project's own settings, and remembering a second layer of
  // preference over them would just be a settings page nobody edited.
  const [projectId, setProjectId] = useState(() => initialProject(projects));
  // One selection covers both: picking a model picks the account it lives
  // under, so there is nothing to keep in step.
  const [chosen, setChosen] = useState<ModelSelection | null>(null);
  const [chosenMode, setChosenMode] = useState("");
  const [chosenEffort, setChosenEffort] = useState<string | null>(null);
  // The 1M context window is a start-time choice (the harness fixes it when the
  // process boots), so it belongs here rather than in the running session.
  // Off by default: 1M is expensive and rarely needed.
  const [want1m, setWant1m] = useState(false);
  const [choice, setChoice] = useState<WorkspaceChoice>({ branch: "", attachPath: "" });
  // "" defers to the project default; picking one pins it for this session
  // only.
  const [chosenKind, setChosenKind] = useState<"" | WorkspaceKind>("");
  // "" defers to the project's base branch, which is what the placeholder in
  // the field says it will do.
  const [baseRef, setBaseRef] = useState("");
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [issues, setIssues] = useState<IssueListing>({ issues: [], issuesError: "" });
  const [loadingSpaces, setLoadingSpaces] = useState(false);
  const [loadingIssues, setLoadingIssues] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const project = projects.find((p) => p.id === projectId) ?? projects[0];
  const instances = pickerInstances(harnesses);
  // Until the user picks, the project's defaults decide — and where it has
  // none, the first account that could actually start a session.
  const fallbackHarness =
    project?.config.defaults.harness ||
    harnesses.find((h) => h.availability.state === "ready")?.id ||
    harnesses[0]?.id ||
    "";
  const instance =
    (chosen && instances.find((i) => i.id === chosen.instance)) ??
    resolveInstance(instances, "", fallbackHarness);
  const harnessId = instance?.driver ?? fallbackHarness;
  const selected = harnesses.find((h) => h.id === harnessId);
  const harnessDefaults = project?.config.defaults.harnesses?.[harnessId];
  // A model the account no longer offers is not sent: the harness's own
  // default is a better answer than a name it has stopped serving.
  const preferred = chosen?.model || (chosen ? "" : (harnessDefaults?.model ?? ""));
  const model = instance?.models.some((m) => m.id === preferred)
    ? preferred
    : (defaultModel(instance)?.id ?? "");
  const selection: ModelSelection = {
    harness: harnessId,
    instance: instance?.id ?? "",
    model,
  };
  // Only Opus 5 offers the 1M window here, and only Claude Code takes the
  // "[1m]" tag. When 1M is wanted, that tag on the model id is what the adapter
  // turns into the context-window setting at start.
  const supports1m = harnessId === "claude" && model.includes("opus");
  const effectiveModel = supports1m && want1m ? `${model}[1m]` : model;
  const modelMeta = instance?.models.find((m) => m.id === model);
  const efforts = modelMeta?.efforts ?? [];
  const preferredEffort = chosenEffort ?? harnessDefaults?.effort ?? "";
  const effort = efforts.includes(preferredEffort) ? preferredEffort : "";
  // Modes are the selected harness's own presets, repopulated when the harness
  // changes — the same shape as the model picker. Only an expressed preference
  // (picked here, or a project default) is sent; otherwise the mode stays ""
  // so the harness's own configured default wins rather than being overridden
  // by an explicit id.
  const modes = selected?.permissionModes ?? [];
  const mode = modes.some((m) => m.id === chosenMode)
    ? chosenMode
    : modes.some((m) => m.id === harnessDefaults?.mode)
      ? (harnessDefaults?.mode ?? "")
      : "";
  const displayModeId = mode || (modes.find((m) => m.default)?.id ?? modes[0]?.id ?? "");
  const modeMeta = modes.find((m) => m.id === displayModeId);
  // The project root is its own choice in the list, so listing it again inside
  // the attach picker would be a second door to the same room.
  const attachable = workspaces.filter((w) => !w.isRoot);
  const kind: WorkspaceKind =
    chosenKind || (project?.config.defaults.workspace === "managed" ? "branch" : "main");

  // Each choice answers all three questions at once, which is the point of
  // making them separate choices: nothing is inferred from an empty field.
  const branch = kind === "branch" ? choice.branch.trim() : "";
  const workspace = kind === "main" ? "local" : kind === "attach" ? "" : "managed";
  const workspacePath = kind === "attach" ? choice.attachPath : "";
  // A base only means anything where omniplex is the one creating the branch.
  const sentBase = kind === "branch" ? baseRef.trim() : "";
  // Branches already on disk are the useful bases — stacking on another
  // worktree's work is exactly what this field exists for. The project default
  // is its own always-present option in the dropdown, so it is filtered out
  // here to avoid listing it twice.
  const baseChoices = Array.from(
    new Set(workspaces.map((w) => w.branch).filter((b): b is string => !!b)),
  ).filter((b) => b !== project?.config.defaults.baseBranch);
  // "branch" can start with an empty name: that is the scratch case, and omniplex
  // makes the name up. Only attach needs a concrete answer before it can go.
  const canStart = kind === "attach" ? !!choice.attachPath : true;
  const ready =
    status === "online" &&
    !!project &&
    instance?.availability?.state === "ready" &&
    canStart &&
    // The busy warning is made of this list. Starting before it arrives is
    // starting without having been told, which is the thing the warning
    // replaced the old hard block with.
    !loadingSpaces;

  const create = async () => {
    if (!project) return;
    setBusy(true);
    setError(null);
    try {
      await onCreate({
        projectId: project.id,
        harness: harnessId,
        instance: instance?.id ?? "",
        model: effectiveModel,
        mode,
        effort,
        branch,
        workspace,
        workspacePath,
        baseRef: sentBase,
      });
      // Remembered on a session that actually started, not on every pick:
      // opening the dropdown, looking, and closing is not a choice worth
      // moving the default for, and neither is a start that errored.
      saveLastProject(project.id);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  // The dialog can mount before the project list has landed, in which case
  // the remembered id had nothing to match and the first project stood in.
  useEffect(() => {
    if (!projectId && projects.length > 0) setProjectId(initialProject(projects));
  }, [projectId, projects]);

  // Worktrees and issues are read per project and re-read whenever the project
  // changes, so a stale list cannot offer a checkout that has since gone.
  useEffect(() => {
    if (!project) return;
    let live = true;
    setLoadingSpaces(true);
    setWorkspaces([]);
    setChoice({ branch: "", attachPath: "" });
    setChosenKind("");
    setBaseRef("");
    onListWorkspaces(project.id)
      .then((r) => {
        if (live) setWorkspaces(r);
      })
      .catch(() => {
        if (live) setWorkspaces([]);
      })
      .finally(() => {
        if (live) setLoadingSpaces(false);
      });
    return () => {
      live = false;
    };
  }, [project?.id, onListWorkspaces]);

  // Issue suggestions are their own request, on their own clock: `gh` may take
  // seconds to answer and nothing here should wait on it.
  useEffect(() => {
    if (!project) return;
    let live = true;
    setLoadingIssues(true);
    setIssues({ issues: [], issuesError: "" });
    onListIssues(project.id)
      .then((r) => {
        if (live) setIssues(r);
      })
      .catch((e) => {
        if (live)
          setIssues({ issues: [], issuesError: e instanceof Error ? e.message : String(e) });
      })
      .finally(() => {
        if (live) setLoadingIssues(false);
      });
    return () => {
      live = false;
    };
  }, [project?.id, onListIssues]);

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      {/* A phone gets the whole screen: this is a task, not an aside, and a
          centred card on a 390px viewport is a card with no margins anyway.
          Header and footer stay put; only the form between them scrolls, so
          "Start" is never scrolled off the bottom. */}
      <DialogContent
        fullscreenOnMobile
        className="flex max-h-[min(85dvh,44rem)] flex-col gap-0 p-0 md:max-w-md"
      >
        <DialogHeader className="px-6 py-4 pt-[calc(1rem+env(safe-area-inset-top))] pr-16 text-left md:pt-4 md:pr-6">
          <DialogTitle>New session</DialogTitle>
          <DialogDescription>
            Pick a project. Omniplex prepares its workspace before starting the agent.
          </DialogDescription>
        </DialogHeader>

        <div className="scroll-thin min-h-0 flex-1 overflow-y-auto px-6 pt-1 pb-5">

        {projects.length === 0 ? (
          <div className="rounded-xl border border-dashed p-6 text-center">
            <p className="text-muted-foreground text-[13px]">
              Add a project once, then create every session from here.
            </p>
            <Button className="mt-4" onClick={onAddProject}>
              <PlusIcon />
              Add project
            </Button>
          </div>
        ) : (
          <div className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="new-session-project">Project</Label>
              <div className="flex gap-2">
                <Select
                  value={project?.id}
                  onValueChange={(v) => {
                    setProjectId(v);
                    setChosen(null);
                    setChosenMode("");
                    setChosenEffort(null);
                  }}
                >
                  <SelectTrigger id="new-session-project" className="min-w-0 flex-1">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {projects.map((p) => (
                      <SelectItem key={p.id} value={p.id}>
                        {p.config.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button
                  variant="outline"
                  size="icon"
                  aria-label="Project settings"
                  title="Project settings"
                  onClick={() => project && onSettings(project)}
                >
                  <SettingsIcon />
                </Button>
                <Button
                  variant="outline"
                  size="icon"
                  aria-label="Add project"
                  title="Add project"
                  onClick={onAddProject}
                >
                  <PlusIcon />
                </Button>
              </div>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="new-session-model">Model</Label>
              {/* Harness and model in one control: choosing a model already
                  chooses the account it runs under, and a session cannot have
                  one without the other. */}
              <ModelPicker
                id="new-session-model"
                harnesses={harnesses}
                value={selection}
                onChange={(next) => {
                  if (next.harness !== harnessId) {
                    setChosenMode("");
                    setChosenEffort(null);
                  }
                  setChosen(next);
                }}
                efforts={efforts}
                effort={effort}
                onEffortChange={setChosenEffort}
              />
              {supports1m && (
                <div className="flex items-center gap-1 pt-1">
                  <span className="text-muted-foreground mr-1 text-[12px]">Context</span>
                  <div className="bg-muted inline-flex rounded-md p-0.5">
                    <button
                      type="button"
                      onClick={() => setWant1m(false)}
                      className={cn(
                        "rounded px-2 py-1 text-[12px] transition-colors",
                        !want1m ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground",
                      )}
                    >
                      200k
                    </button>
                    <button
                      type="button"
                      onClick={() => setWant1m(true)}
                      className={cn(
                        "rounded px-2 py-1 text-[12px] transition-colors",
                        want1m ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground",
                      )}
                    >
                      1M
                    </button>
                  </div>
                  <span className="text-muted-foreground ml-1 text-[11px]">
                    {want1m ? "Larger window, higher cost" : "Standard window"}
                  </span>
                </div>
              )}
            </div>

            {/* Every account that cannot start, not just the chosen one: the
                picker quietly falls back to whatever is ready, so a signed-out
                Claude would otherwise vanish behind a working Codex with no
                word of why. */}
            {instances
              .filter((i) => i.enabled && i.availability?.state !== "ready")
              .map((i) => (
                <Alert key={i.id}>
                  <AlertDescription>
                    <span>
                      {instances.length > 1 && <span className="font-medium">{i.name} — </span>}
                      {i.availability?.reason}
                    </span>
                    <div className="mt-2 flex flex-wrap gap-2">
                      {onLogin && i.availability?.remedy?.some((r) => r.action === "login") && (
                        <Button size="sm" onClick={() => onLogin(i.id)}>
                          <LogInIcon />
                          Sign in
                        </Button>
                      )}
                      <Button variant="outline" size="sm" onClick={onRecheck}>
                        <RefreshCwIcon />
                        Check again
                      </Button>
                    </div>
                  </AlertDescription>
                </Alert>
              ))}

            {modes.length > 0 && (
              <div className="space-y-1.5">
                <Label htmlFor="new-session-mode">Permissions</Label>
                {/* Modes all render alike: the description below says what each
                    one does. Picking one here is the whole decision — no mode
                    earns a badge, a colour, or a second opt-in. */}
                <Select value={displayModeId} onValueChange={setChosenMode}>
                  <SelectTrigger id="new-session-mode" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {modes.map((m) => (
                      <SelectItem key={m.id} value={m.id}>
                        {m.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {modeMeta?.description && (
                  <p className="text-muted-foreground text-[11px]">{modeMeta.description}</p>
                )}
              </div>
            )}

            <fieldset className="space-y-1.5">
              <legend className="text-foreground mb-1.5 text-sm leading-none font-medium">
                Workspace
              </legend>
              {/* One list, one choice, and every scenario has a row of its
                  own. The old two-button toggle left three of them hiding
                  inside a text field that meant something different depending
                  on what you did to it. */}
              <div role="radiogroup" aria-label="Workspace" className="flex flex-col gap-1.5">
                {WORKSPACE_KINDS.map((k) => {
                  const picked = kind === k.id;
                  return (
                    <button
                      key={k.id}
                      type="button"
                      role="radio"
                      aria-checked={picked}
                      onClick={() => {
                        setChosenKind(k.id);
                        // Each choice asks its own question, so it starts from
                        // a blank answer rather than inheriting the last
                        // one's — a branch name is not a worktree path.
                        setChoice({ branch: "", attachPath: "" });
                      }}
                      className={cn(
                        "focus-visible:ring-ring flex min-h-11 flex-col justify-center gap-0.5 rounded-lg border px-3 py-2 text-left transition-colors outline-none focus-visible:ring-2",
                        picked ? "border-primary/60 bg-primary/10" : "hover:bg-accent/50",
                      )}
                    >
                      <span className="text-[13px] leading-tight">{k.label}</span>
                      <span className="text-muted-foreground truncate text-[11px] leading-tight">
                        {k.id === "main" ? (project?.root ?? k.hint) : k.hint}
                      </span>
                    </button>
                  );
                })}
              </div>

              {/* No descriptive copy here: the tile's own hint already names
                  the directory. */}

              {kind === "branch" && (
                <div className="space-y-1.5 pt-1">
                  <Label htmlFor="new-session-workspace">Branch</Label>
                  <WorkspacePicker
                    id="new-session-workspace"
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
              )}

              {kind === "attach" && (
                <div className="space-y-1.5 pt-1">
                  <Label htmlFor="new-session-attach">Worktree</Label>
                  <WorkspacePicker
                    id="new-session-attach"
                    mode="attach"
                    value={choice}
                    onChange={setChoice}
                    workspaces={attachable}
                    issues={issues.issues}
                    issuesError={issues.issuesError}
                    userConfig={userConfig}
                    loading={loadingSpaces}
                    placeholder="Search worktrees"
                  />
                </div>
              )}

              {kind === "branch" && (
                <div className="space-y-1.5 pt-2">
                  <Label htmlFor="new-session-base">Base</Label>
                  {/* A per-session base is what makes stacking possible: a
                      worktree branched from another branch that has not landed
                      yet. A dropdown of the branches already on disk, with the
                      project default as the first, always-present option. */}
                  <Select
                    value={baseRef || BASE_DEFAULT}
                    onValueChange={(v) => setBaseRef(v === BASE_DEFAULT ? "" : v)}
                  >
                    <SelectTrigger id="new-session-base" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={BASE_DEFAULT}>
                        Project default
                        {project?.config.defaults.baseBranch
                          ? ` (${project.config.defaults.baseBranch})`
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
              )}
            </fieldset>
          </div>
        )}

          {error && (
            <Alert variant="destructive" className="mt-4">
              <AlertDescription className="font-mono text-[11px]">{error}</AlertDescription>
            </Alert>
          )}
        </div>

        <DialogFooter className="border-t px-6 py-4 pb-[calc(1rem+env(safe-area-inset-bottom))] md:pb-4">
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          {projects.length > 0 && (
            <Button disabled={!ready || busy} onClick={create}>
              {busy ? "Opening…" : "Start"}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
