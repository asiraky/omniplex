// Browser-local choices, saved as they are selected rather than on session start.
export interface HarnessPrefs {
  instance: string;
  model: string;
  mode: string;
  effort: string;
  want1m: boolean;
}
export interface ProjectPrefs {
  harness: string;
  byHarness: Record<string, HarnessPrefs>;
}
export type SessionPrefs = Record<string, ProjectPrefs>;
const KEY = "omniplex.sessionChoices.v1";

export function loadSessionPrefs(): SessionPrefs {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(KEY) ?? "{}");
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return {};
    const result: SessionPrefs = {};
    for (const [project, value] of Object.entries(raw)) {
      if (!value || typeof value !== "object" || typeof value.harness !== "string") continue;
      const byHarness: Record<string, HarnessPrefs> = {};
      for (const [id, prefs] of Object.entries(value.byHarness ?? {})) {
        const p = prefs as HarnessPrefs | null;
        if (!p || ![p.instance, p.model, p.mode, p.effort].every((v) => typeof v === "string"))
          continue;
        Object.defineProperty(byHarness, id, {
          value: { ...p, want1m: p.want1m === true },
          enumerable: true,
        });
      }
      Object.defineProperty(result, project, {
        value: { harness: value.harness, byHarness },
        enumerable: true,
      });
    }
    return result;
  } catch {
    return {};
  }
}

export function saveSessionPrefs(prefs: SessionPrefs) {
  try {
    localStorage.setItem(KEY, JSON.stringify(prefs));
  } catch {
    // Keep the in-memory choices usable when browser storage is unavailable.
  }
}
