// Mirrors internal/skills: the JSON the skills WS commands return.

export type SkillHarness = "claude" | "codex" | "pi";
export type SkillScope = "project" | "user" | "plugin" | "system";

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

export interface SkillsList {
  skills: Skill[];
  subagents: Subagent[];
  projectRoot?: string;
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

export type SkillFilter = "all" | "project" | "user" | "plugin";

export function matchesFilter(skill: Skill, filter: SkillFilter): boolean {
  if (filter === "all") return true;
  if (filter === "plugin") return skill.scope === "plugin" || skill.scope === "system";
  return skill.scope === filter;
}

export function matchesQuery(skill: Skill, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return (
    skill.name.toLowerCase().includes(q) ||
    skill.description.toLowerCase().includes(q) ||
    (skill.plugin ?? "").toLowerCase().includes(q)
  );
}

export interface SkillSection {
  key: string;
  title: string;
  skills: Skill[];
}

const SCOPE_RANK: Record<SkillScope, number> = { project: 0, user: 1, plugin: 2, system: 3 };

export function scopeLabel(skill: Pick<Skill, "scope" | "plugin">): string {
  switch (skill.scope) {
    case "project":
      return "Project";
    case "user":
      return "Personal";
    case "plugin":
      return skill.plugin ? `Plugin · ${skill.plugin}` : "Plugin";
    default:
      return "System";
  }
}

/**
 * Sections in the server's order: project, personal, one per plugin, system.
 * The server already sorts, so this only has to cut the list where the
 * section changes; a plugin's skills are contiguous because they share a name.
 */
export function groupSkills(skills: Skill[]): SkillSection[] {
  const sorted = [...skills].sort(
    (a, b) =>
      SCOPE_RANK[a.scope] - SCOPE_RANK[b.scope] ||
      (a.plugin ?? "").localeCompare(b.plugin ?? "") ||
      a.name.toLowerCase().localeCompare(b.name.toLowerCase()),
  );
  const sections: SkillSection[] = [];
  for (const skill of sorted) {
    const key = skill.scope === "plugin" ? `plugin:${skill.plugin ?? ""}` : skill.scope;
    let section = sections[sections.length - 1];
    if (!section || section.key !== key) {
      const title =
        skill.scope === "plugin"
          ? `${skill.plugin || "Plugin"} plugin`
          : skill.scope === "system"
            ? "Codex built-in"
            : scopeLabel(skill);
      section = { key, title, skills: [] };
      sections.push(section);
    }
    section.skills.push(skill);
  }
  return sections;
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
