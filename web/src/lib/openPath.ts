import { createContext, useContext } from "react";

/** Open a file (or directory) in the panel, at `line` if given. */
export type OpenPath = (path: string, line?: number) => void;

/**
 * How a rendered file path asks to be opened. Provided by App around the
 * transcript; absent (null) in any context that has nowhere to open a file —
 * where paths render as the plain inline code they were.
 */
export const OpenPathContext = createContext<OpenPath | null>(null);

export function useOpenPath() {
  return useContext(OpenPathContext);
}
