import { useLayoutEffect, useRef } from "react";

/**
 * A ref holding the newest value, for effects and handlers that must see the
 * current callback or prop without re-running when it changes. It is written
 * after commit, not during render, so a render React throws away never leaks
 * into it. Effects in the same component run after the write and see the new
 * value; reading it during render is still wrong.
 */
export function useLatest<T>(value: T) {
  const ref = useRef(value);
  useLayoutEffect(() => {
    ref.current = value;
  });
  return ref;
}
