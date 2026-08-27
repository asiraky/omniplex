// The right panel's tab model. Surfaces are an ordered array with stable ids —
// singletons (`diff`, `files`, `jobs`) plus any number of `file:<path>` and
// `terminal:<n>` tabs — persisted per session, so the panel a session was left
// with is the panel it reopens to.

export type SurfaceKind = "diff" | "files" | "jobs" | "file" | "terminal";

export interface Surface {
  /** Stable id: the kind itself for singletons, `file:<path>`, `terminal:<n>`. */
  id: string;
  kind: SurfaceKind;
  /** file surfaces: the workspace-relative path. */
  path?: string;
}

export interface PanelState {
  surfaces: Surface[];
  active: string;
}

const KEY_PREFIX = "omniplex.panel.v1:";

export function defaultPanel(): PanelState {
  return { surfaces: [{ id: "diff", kind: "diff" }], active: "diff" };
}

export function loadPanel(sessionId: string): PanelState {
  try {
    const raw = localStorage.getItem(KEY_PREFIX + sessionId);
    if (!raw) return defaultPanel();
    const parsed = JSON.parse(raw) as PanelState;
    if (!Array.isArray(parsed.surfaces) || parsed.surfaces.length === 0) return defaultPanel();
    const surfaces = parsed.surfaces
      // The subagents tab was renamed to jobs; a panel saved before that still opens it.
      .map((s) => (s && (s as { kind?: string }).kind === "agents" ? { ...s, id: "jobs", kind: "jobs" } : s))
      .filter(
        (s): s is Surface =>
          !!s &&
          typeof s.id === "string" &&
          ["diff", "files", "jobs", "file", "terminal"].includes(s.kind) &&
          (s.kind !== "file" || typeof s.path === "string"),
      );
    if (surfaces.length === 0) return defaultPanel();
    const active = surfaces.some((s) => s.id === parsed.active) ? parsed.active : surfaces[0].id;
    return { surfaces, active };
  } catch {
    return defaultPanel();
  }
}

export function savePanel(sessionId: string, state: PanelState) {
  try {
    localStorage.setItem(KEY_PREFIX + sessionId, JSON.stringify(state));
  } catch {
    // Storage can be denied outright; the panel still works, it just forgets.
  }
}

/** Add (or focus) a surface. Singletons reuse their id; a file path reuses its tab. */
export function openSurface(state: PanelState, surface: Surface): PanelState {
  const existing = state.surfaces.find((s) => s.id === surface.id);
  if (existing) return { ...state, active: surface.id };
  return { surfaces: [...state.surfaces, surface], active: surface.id };
}

export function closeSurface(state: PanelState, id: string): PanelState {
  const i = state.surfaces.findIndex((s) => s.id === id);
  if (i < 0) return state;
  const surfaces = state.surfaces.filter((s) => s.id !== id);
  if (surfaces.length === 0) return { surfaces, active: "" };
  // Closing the active tab lands on its left neighbour, like every browser.
  const active =
    state.active === id ? surfaces[Math.max(0, i - 1)].id : state.active;
  return { surfaces, active };
}

let terminalSeq = 0;

/** A fresh terminal surface id, unique within this page load. */
export function newTerminalSurface(state: PanelState): Surface {
  // Ids only need to be unique among the surfaces present; scanning is
  // cheaper than persisting a counter.
  let n = ++terminalSeq;
  while (state.surfaces.some((s) => s.id === `terminal:${n}`)) n = ++terminalSeq;
  return { id: `terminal:${n}`, kind: "terminal" };
}

export function fileSurface(path: string): Surface {
  return { id: `file:${path}`, kind: "file", path };
}
