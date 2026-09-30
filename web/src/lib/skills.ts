// Mirrors internal/skills: the JSON the skills WS commands return.

import type { ComposerItem } from "~/protocol";

export type SkillHarness = "claude" | "codex" | "pi";
export type SkillScope = "project" | "user" | "plugin" | "system";
/**
 * How one harness treats a skill it can see. `auto`: the description is in
 * the system prompt. `name-only`: the name is, the description is not.
 * `manual`: neither; only the user runs it, by name. `off`: disabled.
 */
export type InvocationMode = "auto" | "name-only" | "manual" | "off";

export interface HarnessState {
  mode: InvocationMode;
  /** "settings" when harness config, not the skill's own files, decides. */
  by?: string;
}

export interface Source {
  method: "npx" | "git" | "local";
  /** "owner/repo", a URL, or a local path. */
  repo: string;
  ref?: string;
  /** The skill's folder inside the repo, slash-separated. */
  path?: string;
  /** True when Omniplex installed it; false when the skills CLI's lock names it. */
  managed: boolean;
  installedAt?: string;
  updatedAt?: string;
}

export interface Skill {
  name: string;
  description: string;
  /** Symlink-resolved directory: the skill's identity. */
  dir: string;
  scope: SkillScope;
  plugin?: string;
  /** Every discovery path that reaches the skill, before symlink resolution. */
  paths: string[];
  harnesses: SkillHarness[];
  editable: boolean;
  problem?: string;
  /** Synced down from claude.ai rather than written here. */
  synced?: boolean;
  /** One entry per harness that can see the skill. Absent from an older server. */
  invocation?: Partial<Record<SkillHarness, HarnessState>>;
  source?: Source;
  /** Both of the skill's own files say manual-only. */
  manual?: boolean;
}

export interface SkillFile {
  /** Relative to the skill dir, slash-separated. */
  path: string;
  size: number;
}

export interface SkillDetail extends Skill {
  /** SKILL.md, verbatim. */
  content: string;
  files: SkillFile[];
}

export interface Subagent {
  name: string;
  description: string;
  path: string;
  scope: SkillScope;
  harness: SkillHarness;
}

/** How one harness reaches the library. */
export interface Link {
  harness: SkillHarness;
  /** The harness skills dir that reaches, or would reach, the library. */
  dir: string;
  state: "direct" | "per-skill" | "none";
}

export interface GitInfo {
  root: string;
  branch: string;
}

export interface Setup {
  /** As configured, "~"-abbreviated. */
  library: string;
  /** Absolute, symlink-resolved when it exists. */
  libraryDir: string;
  exists: boolean;
  /** Relative to the project root. */
  projectLibrary: string;
  cliVersion: string;
  npx: boolean;
  git?: GitInfo;
  links: Link[];
}

export interface SkillsList {
  skills: Skill[];
  subagents: Subagent[];
  projectRoot?: string;
  /** Absent from an older server. */
  setup?: Setup;
}

export interface SkillFileContent {
  content: string;
  binary: boolean;
}

export const HARNESSES: { id: SkillHarness; label: string }[] = [
  { id: "claude", label: "Claude" },
  { id: "codex", label: "Codex" },
  { id: "pi", label: "pi" },
];

export function harnessLabel(id: SkillHarness): string {
  return HARNESSES.find((h) => h.id === id)?.label ?? id;
}

/**
 * Go marshals an empty slice as null, and a library skill no harness reads has
 * exactly that for `harnesses`. Everything below assumes arrays, so a skill is
 * put through here once, where it arrives.
 */
export function normalizeSkill<T extends Skill>(skill: T): T {
  if (skill.harnesses && skill.paths) return skill;
  return { ...skill, harnesses: skill.harnesses ?? [], paths: skill.paths ?? [] };
}

export function matchesQuery(skill: Skill, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return (
    skill.name.toLowerCase().includes(q) ||
    skill.description.toLowerCase().includes(q) ||
    (skill.plugin ?? "").toLowerCase().includes(q) ||
    (skill.source?.repo ?? "").toLowerCase().includes(q)
  );
}

// ---- grouping by origin ----

export type SkillGroupKind = "project" | "yours" | "source" | "plugins" | "synced" | "system";

/** One row: every copy of a name inside one group. The first copy is the one shown. */
export interface SkillEntry {
  name: string;
  copies: Skill[];
}

export interface SkillSubgroup {
  key: string;
  title: string;
  entries: SkillEntry[];
}

export interface SkillGroup {
  key: string;
  kind: SkillGroupKind;
  title: string;
  /** A source group's repo. */
  repo?: string;
  /** Read-only groups start folded: they are most of the list and none of the work. */
  collapsed: boolean;
  entries: SkillEntry[];
  /** Plugins only: the same entries, cut per plugin. */
  subgroups?: SkillSubgroup[];
}

const GROUP_RANK: Record<SkillGroupKind, number> = {
  project: 0,
  yours: 1,
  source: 2,
  plugins: 3,
  synced: 4,
  system: 5,
};

const GROUP_TITLE: Record<Exclude<SkillGroupKind, "source">, string> = {
  project: "Project",
  yours: "Yours",
  plugins: "Plugins",
  synced: "claude.ai synced",
  system: "Codex built-in",
};

/**
 * Where a skill came from, which is what the list is organised by. A project
 * skill stays under Project even when it was installed from a repo: the
 * project is the more useful answer to "whose is this", and its source is on
 * the detail view.
 */
export function skillOrigin(skill: Skill): { kind: SkillGroupKind; key: string; title: string } {
  let kind: SkillGroupKind;
  if (skill.synced) kind = "synced";
  else if (skill.scope === "plugin") kind = "plugins";
  else if (skill.scope === "system") kind = "system";
  else if (skill.scope === "project") kind = "project";
  else if (skill.source?.repo) {
    return { kind: "source", key: `source:${skill.source.repo}`, title: skill.source.repo };
  } else kind = "yours";
  return { kind, key: kind, title: GROUP_TITLE[kind] };
}

export const byName = (a: string, b: string) => a.toLowerCase().localeCompare(b.toLowerCase());

/** Same-name copies become one entry, the most widely seen copy first. */
function mergeByName(skills: Skill[]): SkillEntry[] {
  const entries = new Map<string, SkillEntry>();
  for (const skill of skills) {
    const key = skill.name.toLowerCase();
    const entry = entries.get(key);
    if (entry) entry.copies.push(skill);
    else entries.set(key, { name: skill.name, copies: [skill] });
  }
  const out = [...entries.values()].sort((a, b) => byName(a.name, b.name));
  for (const entry of out) {
    entry.copies.sort((a, b) => b.harnesses.length - a.harnesses.length || a.dir.localeCompare(b.dir));
  }
  return out;
}

/**
 * The list's groups, in reading order: Project, Yours, one per source repo,
 * then the read-only ones (Plugins, claude.ai synced, Codex built-in).
 */
export function groupSkills(skills: Skill[]): SkillGroup[] {
  const buckets = new Map<string, { origin: ReturnType<typeof skillOrigin>; skills: Skill[] }>();
  for (const skill of skills) {
    const origin = skillOrigin(skill);
    const bucket = buckets.get(origin.key);
    if (bucket) bucket.skills.push(skill);
    else buckets.set(origin.key, { origin, skills: [skill] });
  }
  const groups: SkillGroup[] = [];
  for (const { origin, skills: members } of buckets.values()) {
    const group: SkillGroup = {
      key: origin.key,
      kind: origin.kind,
      title: origin.title,
      collapsed: GROUP_RANK[origin.kind] >= GROUP_RANK.plugins,
      entries: [],
    };
    if (origin.kind === "source") group.repo = origin.title;
    if (origin.kind === "plugins") {
      // A name is merged inside its plugin only: two plugins shipping a skill
      // of the same name are two skills.
      const plugins = new Map<string, Skill[]>();
      for (const skill of members) {
        const plugin = skill.plugin ?? "";
        const list = plugins.get(plugin);
        if (list) list.push(skill);
        else plugins.set(plugin, [skill]);
      }
      group.subgroups = [...plugins.entries()]
        .sort(([a], [b]) => byName(a, b))
        .map(([plugin, list]) => ({
          key: `plugin:${plugin}`,
          title: plugin || "Other plugins",
          entries: mergeByName(list),
        }));
      group.entries = group.subgroups.flatMap((s) => s.entries);
    } else {
      group.entries = mergeByName(members);
    }
    groups.push(group);
  }
  return groups.sort((a, b) => GROUP_RANK[a.kind] - GROUP_RANK[b.kind] || byName(a.title, b.title));
}

/** The copies that share a row with the skill at `dir`; just itself when it is alone or unknown. */
export function copiesOf(groups: SkillGroup[], dir: string): Skill[] {
  for (const group of groups) {
    for (const entry of group.entries) {
      if (entry.copies.some((s) => s.dir === dir)) return entry.copies;
    }
  }
  return [];
}

/** Where a skill sits, for the detail view's badge. */
export function originLabel(skill: Skill): string {
  const origin = skillOrigin(skill);
  if (origin.kind === "plugins") return skill.plugin ? `Plugin: ${skill.plugin}` : "Plugin";
  if (origin.kind === "yours" || origin.kind === "source") return "Personal";
  return origin.title;
}

// ---- invocation ----

const inPrompt = (mode: InvocationMode) => mode === "auto" || mode === "name-only";

/**
 * What a harness does with a skill, or null when it cannot see it. A server
 * that predates `invocation` reports nothing, which reads as the default.
 */
export function harnessState(skill: Skill, harness: SkillHarness): HarnessState | null {
  if (!skill.harnesses.includes(harness)) return null;
  return skill.invocation?.[harness] ?? { mode: "auto" };
}

export interface InvocationSummary {
  /**
   * `auto`: every harness that sees it has it in the prompt (the quiet case).
   * `manual` / `off`: none does. `mixed`: they disagree. `unseen`: no harness
   * reads it at all.
   */
  state: "auto" | "manual" | "off" | "mixed" | "unseen";
  /** Harnesses that cannot see it. */
  missing: SkillHarness[];
  /** Harnesses whose own settings, not the skill's files, decide the mode. */
  overridden: SkillHarness[];
  /** The skill's files say manual for every harness they decide. */
  manual: boolean;
  /** The skill's files disagree between harnesses, which the toggle can repair. */
  fixable: boolean;
}

export function invocationSummary(skill: Skill): InvocationSummary {
  const missing: SkillHarness[] = [];
  const overridden: SkillHarness[] = [];
  const modes: InvocationMode[] = [];
  const fileModes: InvocationMode[] = [];
  for (const { id } of HARNESSES) {
    const state = harnessState(skill, id);
    if (!state) {
      missing.push(id);
      continue;
    }
    modes.push(state.mode);
    if (state.by === "settings") overridden.push(id);
    else fileModes.push(state.mode);
  }
  const shown = modes.filter(inPrompt).length;
  let state: InvocationSummary["state"];
  if (modes.length === 0) state = "unseen";
  else if (shown === modes.length) state = "auto";
  else if (shown > 0) state = "mixed";
  else state = modes.every((m) => m === "off") ? "off" : "manual";

  const fileManual = fileModes.filter((m) => !inPrompt(m)).length;
  return {
    state,
    missing,
    overridden,
    // With no harness reading the files (none linked, or every one
    // overridden by a setting), the files themselves are the answer.
    manual: fileModes.length > 0 ? fileManual === fileModes.length : skill.manual === true,
    fixable: skill.editable && fileManual > 0 && fileManual < fileModes.length,
  };
}

/** A harness's mode in words; null is a harness that cannot see the skill. */
export function modeText(mode: InvocationMode | null): string {
  switch (mode) {
    case "auto":
      return "In the prompt. The model can pick it on its own.";
    case "name-only":
      return "Name in the prompt, description left out.";
    case "manual":
      return "Manual. It runs only when you name it.";
    case "off":
      return "Turned off.";
    default:
      return "Cannot see it.";
  }
}

export function joinWords(words: string[]): string {
  if (words.length <= 1) return words[0] ?? "";
  return `${words.slice(0, -1).join(", ")} and ${words[words.length - 1]}`;
}

// ---- what lands in a harness's system prompt ----

/**
 * The published budgets, as estimates: none of the harnesses report what they
 * actually sent. pi documents no budget, so it has none here.
 */
export const PROMPT_BUDGETS: Record<SkillHarness, { chars?: number; perDescription?: number; note: string }> = {
  claude: {
    chars: 8000,
    perDescription: 1536,
    note: "Claude lists skills in about 1% of the context window, roughly 8,000 characters at 200k, and cuts each description at 1,536 characters.",
  },
  codex: {
    chars: 8000,
    note: "Codex uses 2% of the context window or 8,000 characters. Past that it shortens descriptions, then drops them.",
  },
  pi: { note: "pi does not document a budget." },
};

export interface PromptEntry {
  skill: Skill;
  mode: "auto" | "name-only";
  /** Characters of description that reach the prompt. */
  chars: number;
  /** Characters lost to the harness's per-description cap. */
  cut: number;
}

export interface PromptReport {
  harness: SkillHarness;
  /** Longest first. */
  entries: PromptEntry[];
  total: number;
  /** The harness's overall budget in characters, when it publishes one. */
  budget?: number;
  /** How far `total` runs past the budget; 0 when inside it or when there is none. */
  over: number;
  /** Skills the harness sees but keeps out of the prompt. */
  manual: number;
  off: number;
}

export function promptReport(skills: Skill[], harness: SkillHarness): PromptReport {
  const { chars: budget, perDescription } = PROMPT_BUDGETS[harness];
  const entries: PromptEntry[] = [];
  let manual = 0;
  let off = 0;
  for (const skill of skills) {
    const state = harnessState(skill, harness);
    if (!state) continue;
    if (state.mode === "manual") manual++;
    else if (state.mode === "off") off++;
    else if (state.mode === "name-only") entries.push({ skill, mode: "name-only", chars: 0, cut: 0 });
    else {
      const full = skill.description.length;
      const chars = perDescription === undefined ? full : Math.min(full, perDescription);
      entries.push({ skill, mode: "auto", chars, cut: full - chars });
    }
  }
  entries.sort((a, b) => b.chars - a.chars || byName(a.skill.name, b.skill.name));
  const total = entries.reduce((sum, e) => sum + e.chars, 0);
  return {
    harness,
    entries,
    total,
    budget,
    over: budget === undefined ? 0 : Math.max(0, total - budget),
    manual,
    off,
  };
}

// ---- using a skill in a thread ----

/**
 * The composer's own entry for a skill, which is what knows the token this
 * thread's harness takes ("/name", "$name", a plugin's "plugin:name"). None
 * means the harness running the thread does not offer the skill.
 */
export function composerItemFor(
  items: ComposerItem[],
  skill: Pick<Skill, "name" | "plugin">,
): ComposerItem | undefined {
  const wanted = (skill.plugin ? [`${skill.plugin}:${skill.name}`, skill.name] : [skill.name]).map((n) =>
    n.toLowerCase(),
  );
  const named = (item: ComposerItem, name: string) =>
    item.name.toLowerCase() === name || (item.aliases ?? []).some((a) => a.toLowerCase() === name);
  for (const name of wanted) {
    const hit =
      items.find((item) => item.kind === "skill" && named(item, name)) ??
      items.find((item) => item.behavior === "prompt" && named(item, name));
    if (hit) return hit;
  }
  return undefined;
}

// ---- what the Skills page lists ----

export interface SkillsScope {
  kind: "thread" | "project" | "personal";
  threadId?: string;
  projectId?: string;
  /** The project's name, when the app knows it. */
  projectName?: string;
}

/**
 * The open thread when there is one (its checkout is the project root the
 * harness really sees), else the project being drafted in or last started
 * from, else just the user's own skills.
 */
export function skillsScope(input: {
  threadId?: string | null;
  threadProjectId?: string;
  draftProjectId?: string;
  lastProjectId?: string;
  projects: { id: string; name: string }[];
}): SkillsScope {
  const nameOf = (id?: string) => input.projects.find((p) => p.id === id)?.name;
  if (input.threadId) {
    return { kind: "thread", threadId: input.threadId, projectName: nameOf(input.threadProjectId) };
  }
  for (const id of [input.draftProjectId, input.lastProjectId]) {
    const name = nameOf(id);
    if (id && name !== undefined) return { kind: "project", projectId: id, projectName: name };
  }
  return { kind: "personal" };
}

// ---- installing, committing and updating ----
//
// Only the shapes on the wire live here. What is done with them is in
// `skillFlows.ts`: this module is in the entry bundle, and those helpers are
// wanted only once the Skills page is open.

export type InstallScope = "user" | "project";

export interface StagedSkill {
  name: string;
  description: string;
  /** The skill's folder inside the repo. */
  path?: string;
  /** Includes SKILL.md. */
  files: SkillFile[];
  /** The staged files mark it manual-only. */
  manual: boolean;
  problem?: string;
  /** Named by --skill / -s in the pasted command. */
  picked: boolean;
  /** A folder of this name is already in the personal library. */
  inUser: boolean;
  /** ... or in the project library. */
  inProject: boolean;
}

/** A source fetched into a throwaway dir on the server, waiting to be placed or discarded. */
export interface Staged {
  id: string;
  /** The fetcher that produced it. */
  method: Source["method"];
  repo: string;
  ref?: string;
  skills: StagedSkill[];
  /** e.g. why git was used instead of npx. */
  note?: string;
}

export interface GitChange {
  /** A skill's folder name, or the source record's file name. */
  name: string;
  status: "added" | "modified" | "removed";
  files: number;
}

/** Uncommitted changes in the personal library, one entry per top-level item. */
export interface GitStatus {
  root: string;
  branch: string;
  changes: GitChange[];
}

export interface FileChange {
  path: string;
  status: "added" | "modified" | "removed";
}

export interface UpdateSkill {
  name: string;
  dir: string;
  changed: boolean;
  /** No longer in the source. */
  gone?: boolean;
  /** Upstream is what was installed: the difference is edits made here. */
  local?: boolean;
  files: FileChange[];
}

/** A source fetched again and compared against every skill installed from it. */
export interface UpdateStage {
  id: string;
  repo: string;
  skills: UpdateSkill[];
}

export interface UpdateFile {
  old: string;
  new: string;
  binary: boolean;
}

/** A record's timestamp as a short date; "" when it is missing or not a date. */
export function fmtDate(iso?: string): string {
  if (!iso) return "";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });
}

/** The Agent Skills spec's name rule; "" when valid, else why not. */
export function skillNameError(name: string): string {
  if (name.length === 0) return "A name is required.";
  if (name.length > 64) return "At most 64 characters.";
  if (/[^a-z0-9-]/.test(name)) return "Lowercase letters, digits and hyphens only.";
  if (name.startsWith("-") || name.endsWith("-")) return "Cannot start or end with a hyphen.";
  if (name.includes("--")) return "No double hyphens.";
  return "";
}

export function skillDescriptionError(description: string): string {
  const n = description.trim().length;
  if (n === 0) return "A description is required.";
  if (n > 1024) return "At most 1024 characters.";
  return "";
}

export interface Frontmatter {
  /** Top-level keys in file order; values folded to one display string. */
  fields: [string, string][];
  body: string;
}

/**
 * Splits SKILL.md for the preview: the frontmatter as display pairs, the rest
 * as markdown. Display only — the server is the parser that matters, so a
 * value here is folded and unquoted just enough to read well.
 */
export function splitFrontmatter(content: string): Frontmatter {
  const lines = content.replace(/^﻿/, "").split("\n");
  if (lines[0]?.trimEnd() !== "---") return { fields: [], body: content };
  const end = lines.findIndex((l, i) => i > 0 && l.trimEnd() === "---");
  if (end < 0) return { fields: [], body: content };
  const fields: [string, string][] = [];
  for (const raw of lines.slice(1, end)) {
    const line = raw.replace(/\r$/, "");
    if (!line.trim() || line.trim().startsWith("#")) continue;
    const indented = /^\s/.test(line);
    const last = fields[fields.length - 1];
    if (indented && last) {
      last[1] = last[1] ? `${last[1]} ${line.trim()}` : line.trim();
      continue;
    }
    const colon = line.indexOf(":");
    if (colon < 0) continue;
    let value = line.slice(colon + 1).trim();
    if (/^[>|][+-]?$/.test(value)) value = "";
    fields.push([line.slice(0, colon).trim(), value]);
  }
  for (const f of fields) f[1] = unquote(f[1]);
  return { fields, body: lines.slice(end + 1).join("\n").replace(/^\n+/, "") };
}

function unquote(v: string): string {
  if (v.length >= 2 && v.startsWith('"') && v.endsWith('"')) {
    try {
      return JSON.parse(v) as string;
    } catch {
      return v.slice(1, -1);
    }
  }
  if (v.length >= 2 && v.startsWith("'") && v.endsWith("'")) return v.slice(1, -1).replace(/''/g, "'");
  return v;
}

export function fmtSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
