import { chunk } from "~/lib/chunk";

// The cmdk-built lists of the composer's pickers stay out of the first load.
// Their pickers prefetch them once the page is idle, so a first open rarely
// waits. Kept apart from the components so a test can load them up front.

/** The model picker's search, account rail and model list. */
export const modelList = chunk(() => import("./ModelList").then((m) => m.ModelList));

/** The rows of the composer's slash menu. */
export const commandMenuList = chunk(() =>
  import("./composer/CommandMenuList").then((m) => m.CommandMenuList),
);
