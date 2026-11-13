import type { HarnessMeta, Project } from "~/protocol";

/** Where a setting lives: one of the fixed sections, or a project. */
export type SettingsPlace =
  | { kind: "general" }
  | { kind: "providers" }
  | { kind: "notifications" }
  | { kind: "project"; id: string };

/**
 * One searchable setting. `anchor` is the `data-setting` attribute on the
 * setting's wrapper inside its section, which is what a pick scrolls to.
 */
export interface SettingEntry {
  place: SettingsPlace;
  anchor: string;
  label: string;
  description: string;
  /** The section it is in, as the result shows it: "General", a project's name. */
  where: string;
  /** Words that find it without being on screen: "push" for notifications. */
  keywords?: string[];
}

type Fixed = Omit<SettingEntry, "place" | "where">;

const GENERAL: Fixed[] = [
  {
    anchor: "projects-dir",
    label: "Projects folder",
    description: "Where a new project gets its folder when you give it only a name.",
  },
  {
    anchor: "default-model",
    label: "Default model",
    description: "What a project's first thread starts on.",
    keywords: ["harness", "account"],
  },
  {
    anchor: "default-level",
    label: "Default permissions",
    description: "The permission level a new thread starts at.",
    keywords: ["approval", "sandbox"],
  },
  {
    anchor: "pin-new-threads",
    label: "Pin new threads",
    description: "Whether the pin beside a new thread's composer starts on.",
  },
  {
    anchor: "auto-update-harnesses",
    label: "Keep harness CLIs up to date",
    description: "Runs each agent CLI's own update hourly. Turn off to pin a version.",
    keywords: ["upgrade", "version"],
  },
  {
    anchor: "branch-format",
    label: "Branch names from issues",
    description: "Names the copies started from your open GitHub issues.",
    keywords: ["template", "worktree"],
  },
  {
    anchor: "elevenlabs-key",
    label: "ElevenLabs API key",
    description: "Turns on reading messages aloud and dictation.",
    keywords: ["voice", "speech", "tts"],
  },
];

// Only on screen once there is an ElevenLabs key.
const GENERAL_VOICE: Fixed[] = [
  {
    anchor: "voice",
    label: "Default voice",
    description: "What messages are read aloud in, unless a project picks its own.",
    keywords: ["speech", "tts"],
  },
  {
    anchor: "dictation-cleanup",
    label: "Clean up dictation",
    description: "Fixes punctuation and drops filler words before the text lands.",
    keywords: ["voice", "speech"],
  },
];

const PROVIDERS: Fixed[] = [
  {
    anchor: "add-account",
    label: "Add account",
    description: "Sign an agent in with another account.",
    keywords: ["login", "provider", "instance"],
  },
  {
    anchor: "recheck",
    label: "Check again",
    description: "Look again at which agents are installed and signed in.",
    keywords: ["refresh", "probe"],
  },
];

const NOTIFICATIONS: Fixed[] = [
  {
    anchor: "push-this-device",
    label: "Notifications on this device",
    description: "A notification when an agent asks for you.",
    keywords: ["push", "alert"],
  },
  {
    anchor: "alert-sound",
    label: "Play a sound",
    description: "A short chime when a thread you're not looking at finishes or needs you.",
    keywords: ["audio", "chime"],
  },
  {
    anchor: "devices",
    label: "Devices",
    description: "Every device the server sends notifications to, and a test.",
    keywords: ["push", "test notification"],
  },
];

const PROJECT: Fixed[] = [
  { anchor: "name", label: "Project name", description: "What the project is called." },
  {
    anchor: "folders",
    label: "Folders",
    description: "The folders in this project, and adding one from GitHub or this machine.",
    keywords: ["repo", "repository", "github"],
  },
  {
    anchor: "agent-defaults",
    label: "Agent defaults",
    description:
      "The harness, model, effort, permissions and workspace this project's new threads start on.",
    keywords: ["worktree", "main checkout"],
  },
];

const PROJECT_VOICE: Fixed = {
  anchor: "voice",
  label: "Voice",
  description: "What this project's messages are read aloud in.",
  keywords: ["speech", "tts"],
};

const PROJECT_REMOVE: Fixed = {
  anchor: "remove",
  label: "Remove project",
  description: "Takes the project out of Omniplex. Nothing on disk is touched.",
  keywords: ["delete"],
};

/** A git folder's own settings; the folder id keeps two folders' apart. */
const workspace = (folderId: string): Fixed[] => [
  {
    anchor: `base-branch-${folderId}`,
    label: "Base branch",
    description: "What new copies of this folder branch from.",
    keywords: ["worktree", "main"],
  },
  {
    anchor: `provision-${folderId}`,
    label: "Provision hook",
    description: "A script run in each new copy of this folder before its thread starts.",
    keywords: ["worktree", "setup", "script"],
  },
  {
    anchor: `deprovision-${folderId}`,
    label: "Deprovision hook",
    description: "A script run in a copy of this folder before it is removed.",
    keywords: ["worktree", "teardown", "cleanup", "script"],
  },
];

/**
 * Every setting the screen can show right now. Settings that are not on screen
 * (the voice ones without a key, a workspace for a folder that is not git) are
 * left out, so every result has somewhere to land.
 */
export function settingsIndex({
  projects,
  harnesses,
  hasElevenLabsKey,
}: {
  projects: Project[];
  harnesses: HarnessMeta[];
  hasElevenLabsKey: boolean;
}): SettingEntry[] {
  const at = (place: SettingsPlace, where: string, list: Fixed[]) =>
    list.map((e) => ({ ...e, place, where }));

  const general: SettingsPlace = { kind: "general" };
  const providers: SettingsPlace = { kind: "providers" };
  const out: SettingEntry[] = [
    ...at(general, "General", GENERAL),
    ...(hasElevenLabsKey ? at(general, "General", GENERAL_VOICE) : []),
    ...harnesses.map((h) => ({
      place: providers,
      where: "Providers",
      anchor: `harness-${h.id}`,
      label: h.name,
      description: "The accounts this agent signs in with.",
      // An account is found by its own name too.
      keywords: (h.instances ?? []).map((i) => i.displayName || i.id),
    })),
    ...at(providers, "Providers", PROVIDERS),
    ...at({ kind: "notifications" }, "Notifications", NOTIFICATIONS),
  ];

  for (const p of projects) {
    const place: SettingsPlace = { kind: "project", id: p.id };
    out.push(...at(place, p.name, PROJECT));
    if (hasElevenLabsKey) out.push(...at(place, p.name, [PROJECT_VOICE]));
    const git = p.folders.filter((f) => f.git);
    for (const f of git) {
      // With more than one, the folder says which workspace this is.
      const where = git.length > 1 ? `${p.name} · ${f.path}` : p.name;
      out.push(...at(place, where, workspace(f.id)));
    }
    out.push(...at(place, p.name, [PROJECT_REMOVE]));
  }
  return out;
}

/**
 * The entries every word of the query appears in, across name, description,
 * keywords and section, best first: a name that starts with the query, then a
 * name holding every word, then the rest. Within each, the screen's own order.
 */
export function searchSettings(entries: SettingEntry[], query: string): SettingEntry[] {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  const words = q.split(/\s+/);

  const rank = (e: SettingEntry): number | null => {
    const label = e.label.toLowerCase();
    const text = [label, e.description, e.where, ...(e.keywords ?? [])].join(" ").toLowerCase();
    if (!words.every((w) => text.includes(w))) return null;
    if (label.startsWith(q)) return 0;
    if (words.every((w) => label.includes(w))) return 1;
    return 2;
  };

  return entries
    .map((e, i) => ({ e, i, r: rank(e) }))
    .filter((x): x is { e: SettingEntry; i: number; r: number } => x.r !== null)
    .sort((a, b) => a.r - b.r || a.i - b.i)
    .map((x) => x.e);
}
