import { checkForUpdate } from "./lib/pwa";

// Noticing that the server has been rebuilt.
//
// The document is revalidated on every load, so a reload always picks up the
// current build. That covers a reload, but not a tab that was already open when
// the server was rebuilt.
// This closes that gap, which is what makes "I rebuilt, my phone picked it up"
// true without anyone touching the phone.

/** The bundle this page was built from, or "dev" under the Vite dev server. */
export function currentBuild(): string {
  return document.querySelector<HTMLMetaElement>('meta[name="omniplex-build"]')?.content ?? "dev";
}

// Records which server build we last reloaded for. Keyed by build rather than
// set once, so a second rebuild in the same session still reloads, while a
// reload that fails to resolve a given build is never retried for it.
const RELOADED_FOR = "omniplex.reloadedFor";

/**
 * Reloads when the server reports a bundle other than the one this page is
 * running.
 *
 * Does nothing during development: under Vite the page is "dev" and modules are
 * swapped by HMR, so treating a mismatch as staleness would fight the dev
 * server and reload on every edit.
 */
export function checkBuild(serverBuild: string | undefined) {
  const ours = currentBuild();
  if (!serverBuild || ours === "dev" || serverBuild === ours) return;

  // The server has a different bundle, so any installed service worker is by
  // definition stale too. Asking it to check now rather than on its own
  // schedule means the reload below lands on the new worker instead of being
  // served the old one and having to reload a second time.
  void checkForUpdate();

  try {
    if (sessionStorage.getItem(RELOADED_FOR) === serverBuild) return;
    sessionStorage.setItem(RELOADED_FOR, serverBuild);
  } catch {
    // Without somewhere to record the attempt the reload cannot be bounded,
    // so it is not started. A stale tab is a far smaller problem than a loop.
    return;
  }

  // The document is served no-cache, so a plain reload picks up the new build.
  location.reload();
}
