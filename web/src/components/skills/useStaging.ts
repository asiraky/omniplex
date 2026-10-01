import { useCallback, useEffect, useMemo, useRef } from "react";

import { useLatest } from "~/useLatest";

import type { SkillsCommand } from "./parts";

/**
 * The one staging dir a dialog holds on the server. A fetch can take a minute
 * on a bad link, and the reader may have cancelled, closed the dialog or left
 * the page by the time it lands; whichever happens, the dir is thrown away
 * rather than left for the server's hourly sweep.
 */
export function useStaging(command: SkillsCommand, scopeArgs: Record<string, unknown>) {
  const held = useRef("");
  // Bumped whenever whatever is in flight stops being wanted.
  const token = useRef(0);
  const commandRef = useLatest(command);
  const argsRef = useLatest(scopeArgs);

  const drop = useCallback(
    (id: string) => {
      // Best effort: a dir that is already gone is the outcome being asked for.
      if (id) commandRef.current("discard_staged", { ...argsRef.current, id }).catch(() => {});
    },
    [commandRef, argsRef],
  );

  /** Give up on anything in flight and discard what is held. */
  const release = useCallback(() => {
    token.current++;
    const id = held.current;
    held.current = "";
    drop(id);
  }, [drop]);

  /**
   * Start a fetch. `wanted()` says whether its answer still matters; `hold(id)`
   * takes the dir it produced, or discards it and returns false when the
   * answer came too late.
   */
  const begin = useCallback(() => {
    const mine = ++token.current;
    const wanted = () => mine === token.current;
    const hold = (id: string) => {
      if (!wanted()) {
        drop(id);
        return false;
      }
      drop(held.current);
      held.current = id;
      return true;
    };
    return { wanted, hold };
  }, [drop]);

  /** The server used the dir up (an install deletes it), so there is nothing to discard. */
  const forget = useCallback(() => {
    held.current = "";
  }, []);

  useEffect(() => release, [release]);

  // One object for the life of the component, so it can sit in a dependency list.
  return useMemo(() => ({ begin, release, forget }), [begin, release, forget]);
}
