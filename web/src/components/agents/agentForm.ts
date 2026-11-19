import type { ModelSelection } from "~/components/ModelPicker";
import type { NewAgent } from "~/lib/agentApi";
import { defaultModel, pickerInstances, resolveInstance, resolveModel } from "~/lib/models";
import { modeForLevel } from "~/lib/permissions";
import type { HarnessMeta, PermissionModeMeta, Project, UserConfig } from "~/protocol";

// The plain logic behind the New agent form and the Settings tab, apart from
// the components so those files export only components.

/** What a new agent's compaction instructions start as; the user edits it before creating. */
export const DEFAULT_COMPACT = `This is a long-running agent. Its standing rules live in its system prompt and survive compaction. Do not restate them.

Keep in-flight state: what is in progress, what each piece is waiting on, and decisions the user made that are not in the system prompt yet.

Drop implementation detail, finished work, and tool output that can be read again.`;

/** The New agent form as typed, kept across a reload until it is created. */
export interface AgentDraft {
  name: string;
  projectId: string;
  /** "" is the whole project. */
  folderId: string;
  workspace: "local" | "managed";
  harness: string;
  instance: string;
  model: string;
  effort: string;
  mode: string;
  prompt: string;
  compact: string;
  /** 0 is the harness default. */
  compactAt: number;
  kickoff: string;
}

/**
 * The harness list cut to one account. The Settings tab can change an
 * agent's model but not its harness or account (save_agent has no field for
 * either), so its picker must not offer the other accounts' models.
 */
export function onlyInstance(harnesses: HarnessMeta[], harness: string, instance: string): HarnessMeta[] {
  const own = harnesses.filter((h) => h.id === harness);
  const id = resolveInstance(pickerInstances(own), instance, harness)?.id;
  return own.map((h) => ({ ...h, instances: h.instances.filter((i) => i.id === id) }));
}

/**
 * Classes for an effort Segmented: six levels on a 390px phone cut every label
 * short at the usual padding, so the tabs give up most of it there.
 */
export const EFFORT_FIT = "max-md:[&>button]:px-1.5";

/** The reasoning levels the chosen model takes, most modest first. */
export function modelEfforts(harnesses: HarnessMeta[], sel: ModelSelection): string[] {
  const inst = resolveInstance(pickerInstances(harnesses), sel.instance, sel.harness);
  return resolveModel(inst, sel.model)?.efforts ?? [];
}

export function harnessModes(harnesses: HarnessMeta[], harness: string): PermissionModeMeta[] {
  return harnesses.find((h) => h.id === harness)?.permissionModes ?? [];
}

/**
 * Who a new agent runs as, before the user picks: the project's default
 * harness and its seeds, else the first ready one. Permissions start at
 * "do everything": an agent runs for weeks, and one that has to ask stops
 * and waits every time.
 */
export function initialChoice(
  project: Project | undefined,
  harnesses: HarnessMeta[],
  userConfig: UserConfig | null,
): Pick<AgentDraft, "harness" | "instance" | "model" | "effort" | "mode"> {
  const instances = pickerInstances(harnesses);
  const wanted =
    project?.defaults.harness ||
    instances.find((i) => i.id === userConfig?.defaultInstance)?.driver ||
    harnesses.find((h) => h.availability.state === "ready")?.id ||
    harnesses[0]?.id ||
    "";
  const inst = resolveInstance(instances, userConfig?.defaultInstance ?? "", wanted);
  const harness = inst?.driver ?? wanted;
  const seed = project?.defaults.harnesses?.[harness];
  const model = inst?.models.some((m) => m.id === seed?.model) ? seed!.model! : (defaultModel(inst)?.id ?? "");
  const efforts = resolveModel(inst, model)?.efforts ?? [];
  const effort = seed?.effort && efforts.includes(seed.effort) ? seed.effort : "";
  const modes = harnessModes(harnesses, harness);
  const mode = (seed?.mode && modes.some((m) => m.id === seed.mode) ? seed.mode : "") || modeForLevel(modes, "all");
  return { harness, instance: inst?.id ?? "", model, effort, mode };
}

/** An effort the newly picked model still takes is kept; otherwise it goes back to the harness's own. */
export function keepEffort(harnesses: HarnessMeta[], sel: ModelSelection, effort: string): string {
  return modelEfforts(harnesses, sel).includes(effort) ? effort : "";
}

export function canCreate(d: AgentDraft): boolean {
  return !!d.projectId && !!d.name.trim() && !!d.prompt.trim();
}

/** create_agent's arguments. A first message starts the thread with it; none creates the agent alone. */
export function createArgs(d: AgentDraft): NewAgent {
  const kickoff = d.kickoff.trim();
  return {
    projectId: d.projectId,
    name: d.name.trim(),
    harness: d.harness,
    ...(d.instance ? { instance: d.instance } : {}),
    ...(d.model ? { model: d.model } : {}),
    ...(d.effort ? { effort: d.effort } : {}),
    ...(d.mode ? { mode: d.mode } : {}),
    ...(d.folderId ? { folderId: d.folderId } : {}),
    workspace: d.workspace,
    ...(d.compactAt > 0 ? { compactAt: d.compactAt } : {}),
    ...(kickoff ? { kickoff } : {}),
    prompt: d.prompt,
    compact: d.compact,
    start: !!kickoff,
  };
}

const DRAFT_KEY = "omniplex.newAgentDraft";

/** The draft left from last time, if any: the brief is long to type twice. */
export function loadDraft(): Partial<AgentDraft> | null {
  try {
    const raw = localStorage.getItem(DRAFT_KEY);
    const v: unknown = raw ? JSON.parse(raw) : null;
    return v && typeof v === "object" ? (v as Partial<AgentDraft>) : null;
  } catch {
    return null;
  }
}

export function saveDraft(d: AgentDraft): void {
  try {
    localStorage.setItem(DRAFT_KEY, JSON.stringify(d));
  } catch {
    // Storage full or off: the form still works, it just will not survive a reload.
  }
}

export function clearDraft(): void {
  try {
    localStorage.removeItem(DRAFT_KEY);
  } catch {
    // As above.
  }
}
