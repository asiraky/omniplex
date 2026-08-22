import { useEffect, useState } from "react";

/**
 * Whether the diff viewer soft-wraps long lines. Global on purpose: it is a
 * property of the screen you are reading on, not of a session or a project, so
 * ticking it once should hold everywhere — the same call theme and sidebar
 * width already make.
 */
const KEY = "omniplex.diffWrap";

// Off by default. A diff you can scan by column is the normal way to read one;
// wrapping is the escape hatch for the occasional minified or prose-length line.
const DEFAULT = false;

export function readDiffWrap(): boolean {
  try {
    return localStorage.getItem(KEY) === "1";
  } catch {
    // Storage blocked (Safari private mode). The preference is not worth
    // failing a render over — this session just gets the default.
    return DEFAULT;
  }
}

function storeDiffWrap(wrap: boolean) {
  try {
    localStorage.setItem(KEY, wrap ? "1" : "0");
  } catch {
    /* Not persisted. The choice still applies for this page. */
  }
}

// Every mounted reader moves together, including ones in other tabs, so the
// checkbox never disagrees with the diff next to it.
const listeners = new Set<(wrap: boolean) => void>();

function broadcast(wrap: boolean) {
  for (const listener of listeners) listener(wrap);
}

/** The stored wrap preference, and a setter that persists it. */
export function useDiffWrap(): [boolean, (wrap: boolean) => void] {
  const [wrap, setWrap] = useState(readDiffWrap);

  useEffect(() => {
    listeners.add(setWrap);
    const onStorage = (e: StorageEvent) => {
      if (e.key === KEY) setWrap(readDiffWrap());
    };
    window.addEventListener("storage", onStorage);
    return () => {
      listeners.delete(setWrap);
      window.removeEventListener("storage", onStorage);
    };
  }, []);

  return [
    wrap,
    (next: boolean) => {
      storeDiffWrap(next);
      broadcast(next);
    },
  ];
}
