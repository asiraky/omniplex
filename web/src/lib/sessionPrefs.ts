// What the new-session dialog opened with last time, per project and per
// harness. This replaces the "Agent defaults" grid on the project settings
// screen: the settings a session starts with are a habit, not a configuration,
// and the habit is already expressed by starting a session. Nobody edited that
// grid twice.
//
// Per harness because switching harness in the dialog is switching to a
// different set of names — a Codex effort means nothing to Claude, and a mode
// id from one is not a mode id in the other. Remembering one flat set would
// make every harness switch reset to something arbitrary.
//
// Per browser, like lastProject and sidebar width: it is this device's habit,
// and the phone and the laptop are usually mid-different work. The project's
// own project.json defaults still exist and still seed a project this browser
// has never started a session in.

const KEY = "omniplex.sessionPrefs.v1";

/** What one harness was last started with, in that harness's own vocabulary. */
export interface HarnessPrefs {
  model?: string;
  effort?: string;
  mode?: string;
}

export interface ProjectPrefs {
  harness?: string;
  /** "local" or "managed", the same words project.json uses. An attach is not
   *  recorded: it names a specific checkout, which is a session's business
   *  rather than a standing preference. */
  workspace?: string;
  byHarness: Record<string, HarnessPrefs>;
}

export type SessionPrefs = Record<string, ProjectPrefs>;

const EMPTY: ProjectPrefs = { byHarness: {} };

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function str(v: unknown): string | undefined {
  return typeof v === "string" && v !== "" ? v : undefined;
}

/**
 * Everything remembered, normalised. Anything that is not the shape written
 * here — a half-written key, a value from a future version, hand-edited
 * storage — is dropped rather than trusted: a bad memory must not be able to
 * stop the dialog opening.
 */
export function loadSessionPrefs(): SessionPrefs {
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(KEY);
  } catch {
    // Storage can be denied outright (Safari private mode). The dialog still
    // opens, it just forgets.
    return {};
  }
  if (!raw) return {};
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return {};
  }
  if (!isObject(parsed)) return {};

  const out: SessionPrefs = {};
  for (const [projectId, value] of Object.entries(parsed)) {
    if (!isObject(value)) continue;
    const byHarness: Record<string, HarnessPrefs> = {};
    if (isObject(value.byHarness)) {
      for (const [harnessId, prefs] of Object.entries(value.byHarness)) {
        if (!isObject(prefs)) continue;
        byHarness[harnessId] = {
          model: str(prefs.model),
          effort: str(prefs.effort),
          mode: str(prefs.mode),
        };
      }
    }
    out[projectId] = {
      harness: str(value.harness),
      workspace: str(value.workspace),
      byHarness,
    };
  }
  return out;
}

/** What this project was last started with; empty when it never has been. */
export function projectPrefs(projectId: string): ProjectPrefs {
  if (!projectId) return EMPTY;
  return loadSessionPrefs()[projectId] ?? EMPTY;
}

/** What one harness was last started with in this project. */
export function harnessPrefs(projectId: string, harnessId: string): HarnessPrefs {
  return projectPrefs(projectId).byHarness[harnessId] ?? {};
}

/**
 * Record a session that actually started. Only the named harness's entry is
 * touched, so the other harnesses keep the settings they were last used with.
 * Empty values are recorded as absent: "" means "the harness's own default"
 * everywhere else in the dialog, and it has to keep meaning that on the way
 * back out.
 */
export function saveSessionPrefs(
  projectId: string,
  used: { harness: string; workspace?: string; model?: string; effort?: string; mode?: string },
) {
  if (!projectId || !used.harness) return;
  const all = loadSessionPrefs();
  const project = all[projectId] ?? { byHarness: {} };
  all[projectId] = {
    ...project,
    harness: used.harness,
    // An attach carries no workspace kind worth keeping; leave the last real
    // one in place rather than blanking it.
    workspace: str(used.workspace) ?? project.workspace,
    byHarness: {
      ...project.byHarness,
      [used.harness]: {
        model: str(used.model),
        effort: str(used.effort),
        mode: str(used.mode),
      },
    },
  };
  try {
    localStorage.setItem(KEY, JSON.stringify(all));
  } catch {
    // Costs the memory, not the session that just started.
  }
}
