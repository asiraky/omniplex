import { lazy } from "react";

// An agent's dialogs open from a menu, long after the first paint, and they
// carry the tools page's parts and the form controls. Kept out of the initial
// bundle, which is what a phone on 4G waits for. Render each under Suspense.

export const NewAgentThreadDialog = lazy(() =>
  import("./NewAgentThreadDialog").then((m) => ({ default: m.NewAgentThreadDialog })),
);
export const RestartAgentDialog = lazy(() =>
  import("./AgentDialogs").then((m) => ({ default: m.RestartAgentDialog })),
);
export const ArchiveAgentDialog = lazy(() =>
  import("./AgentDialogs").then((m) => ({ default: m.ArchiveAgentDialog })),
);
