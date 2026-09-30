import { useEffect, useState } from "react";

import { loadThreadPrefs, saveThreadPrefs, type HarnessPrefs } from "~/lib/threadPrefs";

const NO_PREFS: HarnessPrefs = {
  instance: "",
  model: "",
  mode: "",
  effort: "",
  want1m: false,
};

/**
 * The choices this browser made per project: which harness, and per harness
 * the model, mode and effort. Saved as they are picked, not on thread start.
 */
export function useRememberedChoices() {
  const [loaded] = useState(loadThreadPrefs);
  const [preferences, setPreferences] = useState(loaded);

  // Written after the pick lands rather than inside the updater, which React
  // may run twice. What was just read back needs no writing.
  useEffect(() => {
    if (preferences !== loaded) saveThreadPrefs(preferences);
  }, [preferences, loaded]);

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
    saveThreadPrefs({ ...preferences, [projectId]: { harness, byHarness, folderId, copy } });
  };

  return { preferences, patch, recordStart };
}

export type RememberedChoices = ReturnType<typeof useRememberedChoices>;
