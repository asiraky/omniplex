/**
 * Where each thread's diff list was left, per scope: how far it was scrolled
 * and which file was open. Session-only on purpose — this is "put me back
 * where I was" across a tab switch, a closed panel or a hop to another thread,
 * not something worth carrying across a reload.
 */
export interface DiffPlace {
  scrollTop: number;
  expanded: string | null;
  /** A test file a reveal put on screen while tests are hidden. */
  revealed: string | null;
}

const places = new Map<string, DiffPlace>();

const EMPTY: DiffPlace = { scrollTop: 0, expanded: null, revealed: null };

export function diffPlaceKey(threadId: string, scope: string): string {
  return `${threadId}\n${scope}`;
}

export function diffPlace(key: string): DiffPlace {
  return places.get(key) ?? EMPTY;
}

export function rememberDiffPlace(key: string, place: Partial<DiffPlace>) {
  places.set(key, { ...diffPlace(key), ...place });
}

/** For tests: every thread starts where a fresh session would. */
export function forgetDiffPlaces() {
  places.clear();
}
