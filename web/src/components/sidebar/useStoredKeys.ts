import { useCallback, useEffect, useRef, useState } from "react";

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

/**
 * One on/off choice kept in this device's storage under `key`, `fallback`
 * until the user has made it. Only an explicit choice is ever written, so
 * changing the default later moves everyone who never touched it.
 */
export function useStoredFlag(key: string, fallback: boolean) {
  const [loaded] = useState(() => {
    try {
      const raw = localStorage.getItem(key);
      return raw === "true" ? true : raw === "false" ? false : null;
    } catch {
      return null;
    }
  });
  const [flag, setFlag] = useState(loaded);
  // Only a choice made here is written; what was read back needs no writing.
  const chosen = useRef(false);

  useEffect(() => {
    if (!chosen.current || flag === null) return;
    try {
      localStorage.setItem(key, String(flag));
    } catch {
      // Blocked storage: the choice holds for this page.
    }
  }, [key, flag]);

  const choose = useCallback((on: boolean) => {
    chosen.current = true;
    setFlag(on);
  }, []);

  return [flag ?? fallback, choose] as const;
}
