import { useEffect, useState } from "react";

function loadKeys(key: string): Set<string> {
  try {
    const raw = JSON.parse(localStorage.getItem(key) ?? "[]");
    return new Set(Array.isArray(raw) ? raw.filter((k) => typeof k === "string") : []);
  } catch {
    return new Set();
  }
}

/**
 * A set of ids kept in this device's storage under `key`: which labels or
 * projects are hidden, which groups are folded.
 */
export function useStoredKeys(key: string) {
  const [loaded] = useState(() => loadKeys(key));
  const [keys, setKeys] = useState(loaded);

  // Written once the change has landed, not inside the updater, which React
  // may run twice. What was just read back needs no writing.
  useEffect(() => {
    if (keys === loaded) return;
    try {
      localStorage.setItem(key, JSON.stringify([...keys]));
    } catch {
      // Storage can be blocked outright; the filter still works for this page.
    }
  }, [key, keys, loaded]);

  return [keys, setKeys] as const;
}

/** The set with `id` in it or out of it. */
export function withKey(keys: Set<string>, id: string, present: boolean): Set<string> {
  const next = new Set(keys);
  if (present) next.add(id);
  else next.delete(id);
  return next;
}
