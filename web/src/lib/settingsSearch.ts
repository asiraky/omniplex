import type { HarnessMeta, Project } from "~/protocol";

/** One account on the Providers screen. */
export type AccountRef = { harnessId: string; instanceId: string };

/** Where a setting lives: one of the fixed sections, an account, or a project. */
export type SettingsPlace =
  | { kind: "general" }
  /** With an account, that account's own view; without, the list of them. */
  | { kind: "providers"; account?: AccountRef }
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

/** One result's identity, unique across the whole index. */
export function entryKey(e: SettingEntry): string {
  const p = e.place;
  const at =
    p.kind === "project" ? p.id : p.kind === "providers" && p.account ? p.account.instanceId : "";
  return `${p.kind}:${at}:${e.anchor}`;
}

const GENERAL: Fixed[] = [
  {
    anchor: "projects-dir",
    label: "Projects folder",
    description: "Where a new project gets its folder when you give it only a name.",
  },
  {
    anchor: "default-model",
    label: "Default model",
    description:
      "What a project's first thread starts on. Not set, it starts on the first account that is signed in.",
    keywords: ["harness"],
  },
  {
    anchor: "default-level",
    label: "Default permissions",
    description: "The permissions a new thread starts on, or the harness's own default.",
    keywords: ["approval", "sandbox"],
  },
  {
    anchor: "pin-new-threads",
    label: "Pin new threads",
    description:
      "Whether the pin beside a new thread's composer starts on. You can still flip it before sending.",
  },
  {
    anchor: "suggest-prompts",
    label: "Suggest a next prompt",
    description:
      "After a turn, offer the agent's guess at your next message. Takes effect when a session next starts.",
    keywords: ["suggestion", "autocomplete"],
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
    description:
      "Names the copies started from your open GitHub issues. {number} is the issue number and {title} its title.",
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
    description: "What messages are read in, unless a project picks its own.",
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
    anchor: "default-harness",
    label: "Default harness",
    description: "The harness this project's new threads open on.",
    keywords: ["agent defaults"],
  },
  {
    anchor: "harness-settings",
    label: "Harness settings",
    description: "Which harness the model, effort and permissions below are for.",
    keywords: ["agent defaults"],
  },
  {
    anchor: "default-model",
    label: "Default model",
    description: "The model this project's new threads start on, for that harness.",
    keywords: ["agent defaults"],
  },
  {
    anchor: "default-effort",
    label: "Default effort",
    description: "How hard this project's new threads think, for that harness.",
    keywords: ["agent defaults", "reasoning", "thinking"],
  },
  {
    anchor: "default-mode",
    label: "Default permissions",
    description: "The permission mode this project's new threads start on, for that harness.",
    keywords: ["agent defaults", "approval", "sandbox"],
  },
  {
    anchor: "default-workspace",
    label: "Default workspace",
    description: "Whether new threads run in the main checkout or a worktree of their own.",
    keywords: ["agent defaults", "main checkout", "worktree", "copy"],
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
  description: "Takes it out of Omniplex only. Nothing on disk is touched. Its threads go first.",
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
    label: "Provision",
    description: "A script run in each new copy of this folder before its thread starts.",
    keywords: ["worktree", "setup", "script"],
  },
  {
    anchor: `deprovision-${folderId}`,
    label: "Deprovision",
    description: "A script run in a copy of this folder before it is removed.",
    keywords: ["worktree", "teardown", "cleanup", "script"],
  },
];

/** One account's own settings: its view on the Providers screen. */
function accountEntries(h: HarnessMeta): SettingEntry[] {
  return (h.instances ?? []).flatMap((i) => {
    const name = i.displayName || i.id;
    const place: SettingsPlace = {
      kind: "providers",
      account: { harnessId: h.id, instanceId: i.id },
    };
    const where = `Providers · ${name}`;
    const list: Fixed[] = [
      {
        anchor: "account",
        label: name,
        description: `${h.name} account.`,
        keywords: i.account ? [i.id, i.account] : [i.id],
      },
      {
        anchor: "enabled",
        label: "Enabled",
        description: "A disabled account keeps its settings but offers no models.",
      },
      { anchor: "display-name", label: "Display name", description: "What the account is called." },
      ...(h.configFields ?? []).map((f) => ({
        anchor: `field-${f.env}`,
        label: f.label,
        description: f.description ?? "",
        keywords: [f.env],
      })),
      ...(h.modelSettings
        ? [
            {
              anchor: "model-settings",
              label: h.modelSettings.label,
              description: h.modelSettings.description ?? "",
            },
          ]
        : []),
      {
        anchor: "sign-in",
        label: "Sign-in",
        description: "How the account signs in.",
        keywords: ["login", "log in", "auth"],
      },
      {
        anchor: "remove-account",
        label: "Remove account",
        description:
          "Takes the account out of the config. Threads that used it keep their transcripts.",
        keywords: ["delete"],
      },
    ];
    return list.map((e) => ({ ...e, place, where }));
  });
}

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
    })),
    ...at(providers, "Providers", PROVIDERS),
    ...harnesses.flatMap(accountEntries),
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
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return [];
  // One space between words, however many were typed, for the starts-with test.
  const q = words.join(" ");

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
