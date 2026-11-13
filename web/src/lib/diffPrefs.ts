import { useEffect, useState } from "react";

/**
 * How the diff viewer reads: wrapped or not, split or unified, and so on.
 * Global on purpose: these are properties of the screen you are reading on,
 * not of a thread or a project, so ticking one once should hold everywhere —
 * the same call theme and sidebar width already make.
 */
interface Pref {
  key: string;
  fallback: boolean;
  // Every mounted reader moves together, including ones in other tabs, so a
  // toggle never disagrees with the diff next to it.
  listeners: Set<(value: boolean) => void>;
  set: (value: boolean) => void;
}

function pref(key: string, fallback: boolean): Pref {
  const p: Pref = {
    key,
    fallback,
    listeners: new Set(),
    set: (value) => {
      try {
        localStorage.setItem(key, value ? "1" : "0");
      } catch {
        /* Not persisted. The choice still applies for this page. */
      }
      for (const listener of p.listeners) listener(value);
    },
  };
  return p;
}

function read(p: Pref): boolean {
  try {
    const raw = localStorage.getItem(p.key);
    return raw === null ? p.fallback : raw === "1";
  } catch {
    // Storage blocked (Safari private mode). The preference is not worth
    // failing a render over — this page just gets the default.
    return p.fallback;
  }
}

function useBoolPref(p: Pref): [boolean, (value: boolean) => void] {
  const [value, setValue] = useState(() => read(p));

  useEffect(() => {
    p.listeners.add(setValue);
    const onStorage = (e: StorageEvent) => {
      // A null key is another tab clearing storage outright.
      if (e.key === p.key || e.key === null) setValue(read(p));
    };
    window.addEventListener("storage", onStorage);
    return () => {
      p.listeners.delete(setValue);
      window.removeEventListener("storage", onStorage);
    };
  }, [p]);

  return [value, p.set];
}

// All off by default. A unified diff you can scan by column, with nothing
// hidden, is the normal way to read one; each of these is an escape hatch.
const WRAP = pref("omniplex.diffWrap", false);
const SPLIT = pref("omniplex.diffSplit", false);
const IGNORE_WHITESPACE = pref("omniplex.diffIgnoreWhitespace", false);
const HIDE_TESTS = pref("omniplex.diffHideTests", false);
const FULL_CONTEXT = pref("omniplex.diffFullContext", false);

/** Whether long lines soft-wrap, and a setter that persists it. */
export function useDiffWrap(): [boolean, (wrap: boolean) => void] {
  return useBoolPref(WRAP);
}

/** Whether diffs show old and new side by side rather than unified. */
export function useDiffSplit(): [boolean, (split: boolean) => void] {
  return useBoolPref(SPLIT);
}

/** Whether diffs are asked for with whitespace-only changes left out. */
export function useDiffIgnoreWhitespace(): [boolean, (ignore: boolean) => void] {
  return useBoolPref(IGNORE_WHITESPACE);
}

/** Whether test files are left out of the changed-file list. */
export function useDiffHideTests(): [boolean, (hide: boolean) => void] {
  return useBoolPref(HIDE_TESTS);
}

/** Whether diffs show whole files rather than hunks with a few lines around them. */
export function useDiffFullContext(): [boolean, (full: boolean) => void] {
  return useBoolPref(FULL_CONTEXT);
}
