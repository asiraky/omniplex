import { byName, type GitChange, type GitStatus, type Staged, type StagedSkill, type UpdateStage } from "~/lib/skills";

// What the install, commit and update flows work out from the wire types in
// `skills.ts`. Kept apart from it so none of this rides in the entry bundle.

/** Go's nil slices arrive as null. */
export function normalizeStaged(staged: Staged): Staged {
  return {
    ...staged,
    skills: (staged.skills ?? []).map((s) => ({ ...s, files: s.files ?? [], installedIn: s.installedIn ?? [] })),
  };
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

const wantedSkills = (skills: StagedSkill[]) => (skills.length === 1 ? skills : skills.filter((s) => s.picked));

/**
 * The skills ticked when a fetched source first shows: the ones the pasted
 * command named, or the only one there is. A repo of many with none named
 * starts with nothing ticked, since installing all of it is rarely the intent.
 * One that would replace a skill already in the destination always starts
 * unticked.
 */
export function defaultStagedTicks(skills: StagedSkill[], destination: string): string[] {
  return wantedSkills(skills)
    .filter((s) => !s.installedIn.includes(destination))
    .map((s) => s.name);
}

/**
 * The ticks once the destination changes from `from` to `to`. One that would
 * now replace a skill there is unticked; one held back only because it would
 * have replaced one in `from` is ticked again if it was wanted. The rest keep
 * what the reader chose.
 */
export function retickStaged(ticked: ReadonlySet<string>, skills: StagedSkill[], from: string, to: string): string[] {
  const next = new Set(ticked);
  const wanted = new Set(wantedSkills(skills).map((s) => s.name));
  for (const s of skills) {
    const before = s.installedIn.includes(from);
    const after = s.installedIn.includes(to);
    if (after && !before) next.delete(s.name);
    if (before && !after && wanted.has(s.name)) next.add(s.name);
  }
  return skills.filter((s) => next.has(s.name)).map((s) => s.name);
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

export function normalizeUpdateStage(stage: UpdateStage): UpdateStage {
  return { ...stage, skills: (stage.skills ?? []).map((s) => ({ ...s, files: s.files ?? [] })) };
}
