import { ArrowLeftIcon, Maximize2Icon, PanelLeftIcon, XIcon } from "lucide-react";
import { useEffect, useId, useMemo, useState, type FormEvent, type ReactNode } from "react";

import { IconButton } from "~/components/IconButton";
import { ModelPicker } from "~/components/ModelPicker";
import { ErrorLine, Segmented } from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Input } from "~/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "~/components/ui/select";
import { Spinner } from "~/components/ui/spinner";
import { Textarea } from "~/components/ui/textarea";
import type { AgentApi } from "~/lib/agentApi";
import { formatEffort } from "~/lib/efforts";
import { fmtTokens } from "~/lib/format";
import { toast } from "~/lib/toast";
import { cn, errorText } from "~/lib/utils";
import type { Agent, HarnessMeta, Project, UserConfig } from "~/protocol";
import { useIsDesktop } from "~/useMediaQuery";
import {
  canCreate,
  clearDraft,
  createArgs,
  DEFAULT_COMPACT,
  EFFORT_FIT,
  harnessModes,
  initialChoice,
  keepEffort,
  loadDraft,
  modelEfforts,
  saveDraft,
  type AgentDraft,
} from "./agentForm";
import { COMPACT_PRESETS, compactRole } from "./agentText";
import { CLAUDE_MIN_COMPACT_AT, takesCompactAt } from "./compactAt";
import { PreambleDialog, ScopeSelect } from "./shared";

/**
 * The New agent page: who the agent is and where it runs, its brief, and the
 * first message. Typed into a draft that survives a reload, because a brief
 * is long to lose. With a first message, creating also starts its thread.
 */
export function NewAgent({
  projects,
  harnesses,
  userConfig,
  projectId,
  api,
  sidebarOpen,
  onShowSidebar,
  onCancel,
  onCreated,
}: {
  projects: Project[];
  harnesses: HarnessMeta[];
  userConfig: UserConfig | null;
  /** The project whose + was pressed; else the draft's, else the first. */
  projectId?: string;
  api: AgentApi;
  sidebarOpen: boolean;
  onShowSidebar: () => void;
  onCancel: () => void;
  onCreated: (agent: Agent, threadId?: string) => void;
}) {
  const isDesktop = useIsDesktop();
  const [d, setD] = useState<AgentDraft>(() => startDraft(projects, harnesses, userConfig, projectId));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [preamble, setPreamble] = useState(false);
  const [expanded, setExpanded] = useState<"prompt" | "compact" | null>(null);
  const ids = useId();

  // A + on another project while the form is up moves the draft there.
  const [forProject, setForProject] = useState(projectId);
  if (forProject !== projectId) {
    setForProject(projectId);
    if (projectId && projectId !== d.projectId) setD({ ...d, projectId, folderId: "" });
  }
  // The project and harness lists arrive after a reload's first render; the
  // draft waits for them rather than settling on no project and no model.
  if (!d.projectId && projects.length > 0) setD({ ...d, projectId: projects[0].id });
  else if (!d.harness && harnesses.length > 0) {
    setD({ ...d, ...initialChoice(projects.find((p) => p.id === d.projectId), harnesses, userConfig) });
  }

  useEffect(() => saveDraft(d), [d]);

  const set = (patch: Partial<AgentDraft>) => setD((cur) => ({ ...cur, ...patch }));
  const project = projects.find((p) => p.id === d.projectId);
  const sel = { harness: d.harness, instance: d.instance, model: d.model };
  const efforts = useMemo(() => modelEfforts(harnesses, sel), [harnesses, sel.harness, sel.instance, sel.model]); // eslint-disable-line react-hooks/exhaustive-deps
  const modes = harnessModes(harnesses, d.harness);
  const mode = modes.find((m) => m.id === d.mode);
  const kickoff = d.kickoff.trim();
  const ready = canCreate(d) && !busy;

  const submit = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!ready) return;
    setBusy(true);
    setError(null);
    try {
      // A preset picked under another harness stays in the draft for a switch
      // back, but a harness that ignores it is not sent one.
      const res = await api.create(createArgs(takesCompactAt(d.harness) ? d : { ...d, compactAt: 0 }));
      clearDraft();
      onCreated(res.agent, res.threadId);
      // Saved either way, so the form is done with: a retry would make a
      // second agent.
      if (res.startError) {
        toast.error("The agent was saved, but its thread did not start", { description: res.startError });
      } else if (res.promptError) {
        toast.error("The thread started, but the first message did not send", { description: res.promptError });
      }
    } catch (err) {
      setError(errorText(err));
      setBusy(false);
    }
  };

  const folder = project?.folders.find((f) => f.id === d.folderId) ?? project?.folders[0];

  return (
    <section aria-label="New agent" className="flex min-h-0 min-w-0 flex-1 flex-col">
      <header className="flex items-center gap-1 border-b px-2 pt-[calc(0.5rem+env(safe-area-inset-top))] pb-2 md:gap-2 md:px-3">
        {isDesktop && !sidebarOpen && (
          <IconButton label="Show threads" shortcut="toggleSidebar" onClick={onShowSidebar}>
            <PanelLeftIcon className="size-3.5" />
          </IconButton>
        )}
        <IconButton label="Cancel" onClick={onCancel}>
          {isDesktop ? <XIcon /> : <ArrowLeftIcon />}
        </IconButton>
        <h1 className="text-[14px] font-semibold">New agent</h1>
      </header>

      <div className="scroll-thin min-h-0 flex-1 overflow-y-auto">
        <form
          id={`${ids}-form`}
          onSubmit={(e) => void submit(e)}
          className="mx-auto w-full max-w-3xl px-4 pt-5 pb-10 md:px-8"
        >
          <Section
            title="1. Who and where"
            lede="An agent is a long-running thread with a saved brief. It belongs to one project and runs one thread at a time."
          >
            <Field label="Name" htmlFor={`${ids}-name`} hint="Shown in the sidebar and the thread header.">
              <Input
                id={`${ids}-name`}
                value={d.name}
                placeholder="backlog"
                autoComplete="off"
                autoCapitalize="off"
                spellCheck={false}
                onChange={(e) => set({ name: e.target.value })}
                className="h-11 md:h-9"
              />
            </Field>
            <Field label="Project">
              <Select value={d.projectId} onValueChange={(v) => set({ projectId: v, folderId: "" })}>
                <SelectTrigger aria-label="Project" className="h-11 w-full md:h-9">
                  <SelectValue placeholder="Choose a project" />
                </SelectTrigger>
                <SelectContent>
                  {projects.map((p) => (
                    <SelectItem key={p.id} value={p.id}>
                      {p.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            {(project?.folders.length ?? 0) > 1 && (
              <Field label="Scope" hint="Which folder of the project it works in.">
                <ScopeSelect folders={project!.folders} value={d.folderId} onChange={(folderId) => set({ folderId })} />
              </Field>
            )}
            <Field label="Workspace">
              <div role="radiogroup" aria-label="Workspace" className="space-y-2">
                <Option
                  on={d.workspace === "local"}
                  onPick={() => set({ workspace: "local" })}
                  title="Main checkout"
                  hint={folder?.path ?? "The project's own folder."}
                />
                <Option
                  on={d.workspace === "managed"}
                  onPick={() => set({ workspace: "managed" })}
                  title="New worktree"
                  hint="New branch in its own git worktree. The main checkout is untouched."
                />
              </div>
            </Field>
            <Field label="Model">
              <ModelPicker
                harnesses={harnesses}
                value={sel}
                onChange={(next) =>
                  set({
                    ...next,
                    effort: keepEffort(harnesses, next, d.effort),
                    // Modes are the harness's own; another harness starts on its own "do everything".
                    ...(next.harness !== d.harness && {
                      mode: initialChoice(undefined, harnesses.filter((h) => h.id === next.harness), null).mode,
                    }),
                  })
                }
                className="h-11 w-full md:h-9"
              />
              {efforts.length > 0 && (
                <Segmented
                  label="Effort"
                  radio
                  value={d.effort}
                  options={["", ...efforts].map((e) => ({ id: e, label: formatEffort(e) }))}
                  onChange={(effort) => set({ effort })}
                  className={cn("mt-2", EFFORT_FIT)}
                />
              )}
            </Field>
            {modes.length > 0 && (
              <Field
                label="Permissions"
                hint={[
                  mode?.description,
                  mode && mode.level !== "all" && "An agent that runs for weeks will stop and wait every time it has to ask.",
                ]
                  .filter(Boolean)
                  .join(" ")}
              >
                {/* Radix's hidden native select reports "" when the value and the
                    options change in one render, as they do on a harness switch,
                    which would undo the switch's "do everything". No mode is "". */}
                <Select value={d.mode || modes[0].id} onValueChange={(v) => v && set({ mode: v })}>
                  <SelectTrigger aria-label="Permissions" className="h-11 w-full md:h-9">
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
              </Field>
            )}
          </Section>

          <Section
            title="2. The brief"
            lede="The system prompt holds everything the agent must never forget. Compaction does not touch it. The compaction instructions say what a summary keeps and drops."
          >
            <Field
              label="System prompt"
              htmlFor={`${ids}-prompt`}
              aside={<ExpandButton label="Expand system prompt" onClick={() => setExpanded("prompt")} />}
              hint={
                <>
                  Saved as revision r1. Omniplex adds a short fixed preamble in front of it.{" "}
                  <button type="button" onClick={() => setPreamble(true)} className="text-primary underline-offset-2 hover:underline">
                    Read the preamble
                  </button>
                </>
              }
            >
              <Textarea
                id={`${ids}-prompt`}
                value={d.prompt}
                placeholder={"# What this agent does\n\nStanding rules, one per line."}
                onChange={(e) => set({ prompt: e.target.value })}
                rows={isDesktop ? 12 : 8}
                className="font-mono text-[12.5px] leading-relaxed [field-sizing:fixed]"
              />
            </Field>
            <Field
              label="Compaction instructions"
              htmlFor={`${ids}-compact`}
              aside={
                <>
                  {d.compact === DEFAULT_COMPACT ? (
                    <span className="text-muted-foreground rounded border px-1.5 font-mono text-[10.5px]">default</span>
                  ) : (
                    <Button type="button" variant="ghost" size="sm" onClick={() => set({ compact: DEFAULT_COMPACT })}>
                      Reset to default
                    </Button>
                  )}
                  <ExpandButton label="Expand compaction instructions" onClick={() => setExpanded("compact")} />
                </>
              }
              hint={`Saved as c1. ${compactRole(d.harness)}`}
            >
              <Textarea
                id={`${ids}-compact`}
                value={d.compact}
                onChange={(e) => set({ compact: e.target.value })}
                rows={isDesktop ? 7 : 6}
                className="font-mono text-[12.5px] leading-relaxed [field-sizing:fixed]"
              />
            </Field>
            {takesCompactAt(d.harness) && (
              <Field label="Compact at" hint={compactHint(d.compactAt, d.harness)}>
                <Segmented
                  label="Compact at"
                  radio
                  value={String(d.compactAt)}
                  options={[
                    { id: "0", label: "Default" },
                    ...COMPACT_PRESETS.map((n) => ({ id: String(n), label: fmtTokens(n) })),
                  ]}
                  onChange={(v) => set({ compactAt: Number(v) })}
                />
              </Field>
            )}
          </Section>

          <Section
            title="3. First message"
            lede="Sent as the first user message. Leave it empty to create the agent without starting a thread."
          >
            <Field label="First message" htmlFor={`${ids}-kickoff`} hideLabel hint="Saved on the agent and offered again whenever you start a new thread.">
              <Textarea
                id={`${ids}-kickoff`}
                value={d.kickoff}
                placeholder="Start the backlog loop. Poll now, then every 10 minutes."
                onChange={(e) => set({ kickoff: e.target.value })}
                rows={3}
              />
            </Field>
          </Section>
          {error && <ErrorLine message={error} />}
        </form>
      </div>

      <footer className="flex items-center justify-end gap-2 border-t px-4 pt-3 pb-[calc(0.75rem+env(safe-area-inset-bottom))]">
        <Button type="button" variant="ghost" onClick={onCancel} className="h-11 md:h-9">
          Cancel
        </Button>
        <Button type="submit" form={`${ids}-form`} disabled={!ready} className="h-11 flex-1 md:h-9 md:flex-none">
          {busy && <Spinner aria-hidden className="size-4" />}
          {kickoff ? "Create and start" : "Create agent"}
        </Button>
      </footer>

      <PreambleDialog api={api} open={preamble} onOpenChange={setPreamble} />
      {expanded && (
        <ExpandedEditor
          title={expanded === "prompt" ? "System prompt" : "Compaction instructions"}
          value={expanded === "prompt" ? d.prompt : d.compact}
          onDone={(v) => {
            set(expanded === "prompt" ? { prompt: v } : { compact: v });
            setExpanded(null);
          }}
          onClose={() => setExpanded(null)}
        />
      )}
    </section>
  );
}

/** The draft to open on: last time's, unless it was for another project than the + pressed. */
function startDraft(
  projects: Project[],
  harnesses: HarnessMeta[],
  userConfig: UserConfig | null,
  projectId: string | undefined,
): AgentDraft {
  const saved = loadDraft();
  const pid = projectId ?? saved?.projectId ?? projects[0]?.id ?? "";
  const project = projects.find((p) => p.id === pid);
  const fresh: AgentDraft = {
    name: "",
    projectId: pid,
    folderId: "",
    workspace: project?.defaults.workspace === "managed" ? "managed" : "local",
    ...initialChoice(project, harnesses, userConfig),
    prompt: "",
    compact: DEFAULT_COMPACT,
    compactAt: 0,
    kickoff: "",
  };
  if (!saved) return fresh;
  const merged = { ...fresh, ...saved, projectId: pid };
  // The folder belongs to the saved project; another project starts whole.
  if (saved.projectId !== pid) merged.folderId = "";
  return merged;
}

function compactHint(at: number, harness: string): string {
  const head = at
    ? `Compacts when context reaches ${fmtTokens(at)} tokens.${harness === "claude" ? ` The minimum is ${fmtTokens(CLAUDE_MIN_COMPACT_AT)}.` : ""}`
    : "Default follows the harness.";
  return `${head} Lower means more compactions and less to lose in each one.`;
}

function Section({ title, lede, children }: { title: string; lede: string; children: ReactNode }) {
  return (
    <section className="mb-8">
      <h2 className="text-[15px] font-semibold">{title}</h2>
      <p className="text-muted-foreground mt-1 mb-3.5 text-[13px] leading-relaxed">{lede}</p>
      {children}
    </section>
  );
}

function Field({
  label,
  htmlFor,
  hint,
  aside,
  hideLabel,
  children,
}: {
  label: string;
  htmlFor?: string;
  hint?: ReactNode;
  aside?: ReactNode;
  hideLabel?: boolean;
  children: ReactNode;
}) {
  return (
    // relative: a hidden label is absolutely placed, and with no positioned
    // ancestor it lands against the page and stretches the document.
    <div className="relative mb-4">
      <div className={cn("mb-1.5 flex min-h-6 items-center gap-1", hideLabel && "sr-only")}>
        {htmlFor ? (
          <label htmlFor={htmlFor} className="flex-1 text-[14px] font-medium">
            {label}
          </label>
        ) : (
          <span className="flex-1 text-[14px] font-medium">{label}</span>
        )}
        {aside}
      </div>
      {children}
      {hint && <p className="text-muted-foreground mt-1.5 text-[12px] leading-relaxed">{hint}</p>}
    </div>
  );
}

function Option({ on, onPick, title, hint }: { on: boolean; onPick: () => void; title: string; hint: string }) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={on}
      onClick={onPick}
      className={cn(
        "focus-visible:ring-ring flex w-full min-w-0 flex-col rounded-lg border px-3 py-2 text-left outline-none focus-visible:ring-2",
        on ? "border-primary bg-primary/10" : "hover:bg-accent/40",
      )}
    >
      <b className="text-[13.5px] font-medium">{title}</b>
      <span className="text-muted-foreground truncate text-[12px]">{hint}</span>
    </button>
  );
}

function ExpandButton({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <Button type="button" variant="ghost" size="sm" aria-label={label} onClick={onClick} className="text-muted-foreground">
      <Maximize2Icon className="size-3" />
      Expand
    </Button>
  );
}

/** A long text in the whole screen, for a brief too long for its box. Done keeps the edit; closing drops it. */
function ExpandedEditor({
  title,
  value,
  onDone,
  onClose,
}: {
  title: string;
  value: string;
  onDone: (value: string) => void;
  onClose: () => void;
}) {
  const [text, setText] = useState(value);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent fullscreenOnMobile className="flex h-[90dvh] flex-col gap-3 sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription className="sr-only">Edit the whole text.</DialogDescription>
        </DialogHeader>
        <Textarea
          aria-label={title}
          value={text}
          onChange={(e) => setText(e.target.value)}
          autoFocus
          className="min-h-0 flex-1 resize-none font-mono leading-relaxed [field-sizing:fixed] md:text-[12.5px]"
        />
        <DialogFooter>
          <Button variant="outline" onClick={onClose} className="min-h-11 md:min-h-9">
            Cancel
          </Button>
          <Button onClick={() => onDone(text)} className="min-h-11 md:min-h-9">
            Done
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
