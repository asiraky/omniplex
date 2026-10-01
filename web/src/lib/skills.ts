// Mirrors internal/skills: the JSON the skills WS commands return.

export type SkillScope = "project" | "user" | "plugin" | "system";

/**
 * What agents do with a skill. `on`: they use it when it fits. `manual`: it
 * runs only when asked for by name. `off`: no agent can see it.
 */
export type SkillMode = "on" | "manual" | "off";

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
  editable: boolean;
  problem?: string;
  /** Synced down from claude.ai rather than written here. */
  synced?: boolean;
  mode: SkillMode;
  source?: Source;
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

export interface SkillsList {
  skills: Skill[];
  projectRoot?: string;
  projectName?: string;
  /** Whether Claude loads the skills it syncs from the claude.ai account. */
  claudeSync: boolean;
  /** Whether Codex loads its own built-in skills. */
  codexBundled: boolean;
}

export interface SkillFileContent {
  content: string;
  binary: boolean;
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

// ---- the list's sections ----

export type SectionKind = "yours" | "project" | "synced" | "system" | "plugins";

export const SECTION_ORDER: SectionKind[] = ["yours", "project", "synced", "system", "plugins"];

export function sectionOf(skill: Skill): SectionKind {
  if (skill.synced) return "synced";
  if (skill.scope === "plugin") return "plugins";
  if (skill.scope === "system") return "system";
  if (skill.scope === "project") return "project";
  return "yours";
}

export const byName = (a: string, b: string) => a.toLowerCase().localeCompare(b.toLowerCase());

/**
 * Every skill in its section, each section sorted by name. Same-name copies
 * stay apart: each directory is its own row. Plugins sort by plugin first.
 */
export function sectionSkills(skills: Skill[]): Record<SectionKind, Skill[]> {
  const out: Record<SectionKind, Skill[]> = { yours: [], project: [], synced: [], system: [], plugins: [] };
  for (const skill of skills) out[sectionOf(skill)].push(skill);
  for (const kind of SECTION_ORDER) {
    out[kind].sort(
      (a, b) =>
        (kind === "plugins" ? byName(a.plugin ?? "", b.plugin ?? "") : 0) ||
        byName(a.name, b.name) ||
        a.dir.localeCompare(b.dir),
    );
  }
  return out;
}

/** Where a skill is from, in the detail view's one line. */
export function originText(skill: Skill): string {
  let text: string;
  switch (sectionOf(skill)) {
    case "synced":
      text = "From claude.ai";
      break;
    case "plugins":
      text = skill.plugin ? `Plugin ${skill.plugin}` : "Plugin";
      break;
    case "system":
      text = "Codex built-in";
      break;
    case "project":
      text = "Project";
      break;
    default:
      text = skill.source?.repo ? `Yours, from ${skill.source.repo}` : "Yours";
  }
  return skill.editable ? text : `${text}, read-only`;
}

export const MODE_TEXT: Record<SkillMode, string> = {
  on: "Agents use it when it fits.",
  manual: "Only runs when you ask for it by name.",
  off: "Agents can't see it.",
};

export const MODE_LABEL: Record<SkillMode, string> = { on: "On", manual: "Manual", off: "Off" };

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
 * agent really sees), else the project being drafted in or last started
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

export interface StagedSkill {
  name: string;
  description: string;
  /** Includes SKILL.md. */
  files: SkillFile[];
  problem?: string;
  /** Named by --skill / -s in the pasted command. */
  picked?: boolean;
  /** A skill of this name is already in the personal library. */
  installed: boolean;
}

/** A source fetched into a throwaway dir on the server, waiting to be installed or discarded. */
export interface Staged {
  id: string;
  repo: string;
  ref?: string;
  skills: StagedSkill[];
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

/** A source fetched again and compared against what was installed from it. */
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
 * as markdown. Display only: the server is the parser that matters, so a
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
