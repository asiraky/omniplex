import {
  byName,
  HARNESSES,
  type GitChange,
  type GitStatus,
  type InstallScope,
  type Setup,
  type SkillHarness,
  type Staged,
  type StagedSkill,
  type UpdateSkill,
  type UpdateStage,
} from "~/lib/skills";

// What the install, commit and update flows work out from the wire types in
// `skills.ts`. Kept apart from it so none of this rides in the entry bundle.

/** Go's nil slices arrive as null; see normalizeSkill. */
export function normalizeStaged(staged: Staged): Staged {
  return { ...staged, skills: (staged.skills ?? []).map((s) => ({ ...s, files: s.files ?? [] })) };
}

/**
 * What to call the thing being fetched while it is fetched. The server is the
 * parser that matters; this only has to name the repo in whatever was pasted:
 * a whole `npx skills add` line, a GitHub page URL, `owner/repo#ref`, a folder.
 */
export function sourceLabel(source: string): string {
  let text = source.trim();
  const words = text.split(/\s+/);
  const add = words.indexOf("add");
  if (add > 0 && words.slice(0, add).some((w) => /^skills(@.+)?$/.test(w))) {
    text = words.slice(add + 1).find((w) => !w.startsWith("-")) ?? text;
  }
  if (text.startsWith("/") || text.startsWith("~")) return text;
  const github = /github\.com[/:]([^/\s]+\/[^/\s#]+)/.exec(text);
  if (github) return github[1].replace(/\.git$/, "");
  return text.replace(/#.*$/, "").replace(/\.git$/, "");
}

/**
 * The skills ticked when a staged source first shows: the ones the pasted
 * command named, or the only one there is. A repo of many with none named
 * starts with nothing ticked, since installing all of it is rarely the intent.
 */
export function defaultStagedTicks(skills: StagedSkill[]): string[] {
  if (skills.length === 1) return [skills[0].name];
  return skills.filter((s) => s.picked).map((s) => s.name);
}

/** The ticked skills whose name is already taken in the library being installed into. */
export function stagedClashes(skills: StagedSkill[], ticked: Iterable<string>, scope: InstallScope): string[] {
  const chosen = new Set(ticked);
  return skills.filter((s) => chosen.has(s.name) && (scope === "user" ? s.inUser : s.inProject)).map((s) => s.name);
}

/** The folder an install of this scope lands in, as a path the reader can recognise. */
export function installPath(scope: InstallScope, setup?: Setup, projectRoot?: string): string {
  if (!setup) return "";
  if (scope === "user") return setup.libraryDir || setup.library;
  return projectRoot ? `${projectRoot.replace(/\/+$/, "")}/${setup.projectLibrary}` : setup.projectLibrary;
}

export interface InstallLink {
  harness: SkillHarness;
  /** The harness reads the library itself, so installing there is enough. */
  direct: boolean;
  /** Where the symlink goes when it does not. */
  dir: string;
  text: string;
}

/** The folder each harness reads inside a project, relative to its root. */
const PROJECT_LINK_DIR: Record<SkillHarness, string> = {
  claude: ".claude/skills",
  codex: ".agents/skills",
  pi: ".agents/skills",
};

const trimRelative = (path: string) => path.replace(/^(\.\/)+/, "").replace(/\/+$/, "");

/**
 * What installing does for each harness. For the personal library the server
 * has detected it (`setup.links`). For a project it reports nothing, but the
 * rule is fixed: a harness reads the project library exactly when that is the
 * folder it looks in, and gets a per-skill symlink in its own folder otherwise.
 */
export function installLinks(setup: Setup | undefined, scope: InstallScope): InstallLink[] {
  if (!setup) return [];
  const out: InstallLink[] = [];
  for (const { id } of HARNESSES) {
    let direct: boolean;
    let dir: string;
    if (scope === "project") {
      dir = PROJECT_LINK_DIR[id];
      direct = trimRelative(setup.projectLibrary) === dir;
    } else {
      const link = (setup.links ?? []).find((l) => l.harness === id);
      if (!link) continue;
      dir = link.dir;
      direct = link.state === "direct";
    }
    out.push({ harness: id, direct, dir, text: direct ? "Already reads the library" : `Adds a symlink in ${dir}` });
  }
  return out;
}

export function normalizeGitStatus(git: GitStatus | null | undefined): GitStatus | null {
  return git ? { ...git, changes: git.changes ?? [] } : null;
}

/** The source record: it changes whenever a skill is installed, updated or removed. */
export const RECORD_FILE = ".omniplex-skills.json";

/** Past this many names a verb counts its skills instead: a subject line has to stay one line. */
const MESSAGE_NAMES = 4;

/**
 * The commit message for a set of changes, in the library's own style:
 * `skills: add <name>`, `skills: update <a>, <b>`, `skills: remove <name>`,
 * and the verbs joined with semicolons when they mix. The source record rides
 * along with the skills it describes, so it is named only when it is alone.
 */
export function defaultCommitMessage(changes: GitChange[]): string {
  if (changes.length === 0) return "";
  const named = changes.filter((c) => c.name !== RECORD_FILE);
  if (named.length === 0) return "skills: update source record";
  const verbOf = (c: GitChange) => (c.status === "added" ? "add" : c.status === "removed" ? "remove" : "update");
  const parts: string[] = [];
  for (const verb of ["add", "update", "remove"]) {
    const names = named
      .filter((c) => verbOf(c) === verb)
      .map((c) => c.name)
      .sort(byName);
    if (names.length === 0) continue;
    parts.push(`${verb} ${names.length > MESSAGE_NAMES ? `${names.length} skills` : names.join(", ")}`);
  }
  return `skills: ${parts.join("; ")}`;
}

/** The last segment of a path: a repo's folder name is what people call it. */
export function baseName(path: string): string {
  return path.replace(/\/+$/, "").split("/").pop() || path;
}

export function normalizeUpdateStage(stage: UpdateStage): UpdateStage {
  return { ...stage, skills: (stage.skills ?? []).map((s) => ({ ...s, files: s.files ?? [] })) };
}

/** The skills an update would change that have not been updated yet. */
export function pendingUpdates(stage: UpdateStage, applied: ReadonlySet<string>): UpdateSkill[] {
  return stage.skills.filter((s) => s.changed && !s.gone && !applied.has(s.dir));
}
