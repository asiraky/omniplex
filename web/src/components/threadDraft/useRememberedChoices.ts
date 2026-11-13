import { useEffect, useState } from "react";

import {
  loadThreadPrefs,
  saveThreadPrefs,
  type HarnessPrefs,
  type ThreadPrefs,
} from "~/lib/threadPrefs";

const NO_PREFS: HarnessPrefs = {
  instance: "",
  model: "",
  mode: "",
  effort: "",
  want1m: false,
  fast: false,
};

/** Choices to open on in one project, over what it last used. */
export interface ChoiceSeed {
  projectId: string;
  harness: string;
  /** Its mode stands in only for a harness with nothing saved: the
      permissions remembered are the person's, not what a thread ran. */
  prefs: HarnessPrefs;
}

/**
 * The choices this browser made per project: which harness, and per harness
 * the model, mode and effort. Saved as they are picked, not on thread start.
 *
 * A seed is laid over what was read, as if it had been read: opening on it
 * writes nothing, and a pick saves on top of it. Left untouched, it is never
 * written: a pick or a start in another project saves that project, not the
 * seed in its place.
 */
export function useRememberedChoices(seed?: ChoiceSeed) {
  const [opened] = useState(() => {
    const read = loadThreadPrefs();
    return { read, loaded: withSeed(read, seed), seeded: seed?.projectId };
  });
  const { loaded } = opened;
  const [preferences, setPreferences] = useState(loaded);

  // Written after the pick lands rather than inside the updater, which React
  // may run twice. What was just read back needs no writing.
  useEffect(() => {
    if (preferences !== loaded) saveThreadPrefs(unseeded(preferences, opened));
  }, [preferences, loaded, opened]);

  // A patch over the latest saved choices, not this render's: one pick can
  // change the model and then the effort, and the second must not undo the
  // first. `base` stands in for a harness with nothing saved yet.
  const patch = (
    projectId: string,
    harness: string,
    change: Partial<HarnessPrefs>,
    base?: HarnessPrefs,
  ) =>
    setPreferences((prev) => {
      const saved = prev[projectId];
      const old = saved?.byHarness[harness] ?? base;
      return {
        ...prev,
        [projectId]: {
          ...saved,
          harness,
          byHarness: {
            ...saved?.byHarness,
            [harness]: { ...(old ?? NO_PREFS), ...change },
          },
        },
      };
    });

  // Remembered on a thread that actually started, not on every pick.
  const recordStart = (
    projectId: string,
    started: { harness: string; folderId: string; copy?: boolean },
  ) => {
    const { harness, folderId, copy } = started;
    const byHarness = preferences[projectId]?.byHarness ?? {};
    saveThreadPrefs(unseeded({ ...preferences, [projectId]: { harness, byHarness, folderId, copy } }, opened));
  };

  return { preferences, patch, recordStart };
}

export type RememberedChoices = ReturnType<typeof useRememberedChoices>;

function withSeed(saved: ThreadPrefs, seed: ChoiceSeed | undefined): ThreadPrefs {
  if (!seed) return saved;
  const { projectId, harness, prefs } = seed;
  const project = saved[projectId];
  const mode = project?.byHarness[harness]?.mode ?? prefs.mode;
  return {
    ...saved,
    [projectId]: {
      ...project,
      harness,
      byHarness: { ...project?.byHarness, [harness]: { ...prefs, mode } },
    },
  };
}

// The seeded project as it was read, while the seed is all it holds.
function unseeded(
  prefs: ThreadPrefs,
  { read, loaded, seeded }: { read: ThreadPrefs; loaded: ThreadPrefs; seeded?: string },
): ThreadPrefs {
  if (!seeded || prefs[seeded] !== loaded[seeded]) return prefs;
  const out = { ...prefs };
  if (read[seeded]) out[seeded] = read[seeded];
  else delete out[seeded];
  return out;
}
