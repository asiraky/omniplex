import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import type { ComposerItem } from "~/protocol";

/** The provider's slash-command catalogue as the composer reads it. */
export interface ComposerCatalogue {
  items: ComposerItem[];
  loading: boolean;
  /** False until a catalogue has loaded: until then slash text is held back,
      because whether it is a command or a prompt is not known yet. */
  ready: boolean;
  /** Asks for the catalogue again, keeping the current one until it lands. */
  reload: () => void;
}

/** A composer with no provider behind it: nothing to load, nothing to wait for. */
export const NO_CATALOGUE: ComposerCatalogue = {
  items: [],
  loading: false,
  ready: true,
  reload: () => {},
};

/**
 * Fetches the catalogue on mount and whenever `load` changes: a new loader is
 * how the app says the provider's catalogue changed. Owned by whatever renders
 * the composer, keyed per thread there, so a thread switch starts empty. A
 * load that failed is tried again when a trigger opens (the composer calls
 * `reload`), so nothing here retries on its own.
 */
export function useComposerItems(load: () => Promise<ComposerItem[]>): ComposerCatalogue {
  const [items, setItems] = useState<ComposerItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [ready, setReady] = useState(false);
  const loadSequence = useRef(0);

  const reload = useCallback(() => {
    const sequence = ++loadSequence.current;
    setLoading(true);
    load()
      .then((next) => {
        if (sequence === loadSequence.current) {
          setItems(next);
          setReady(true);
        }
      })
      .catch(() => {
        // Retain a previously successful catalogue. If the first request
        // failed, ready remains false and slash text is not sent as a prompt
        // while its behavior is unknown.
      })
      .finally(() => {
        if (sequence === loadSequence.current) setLoading(false);
      });
  }, [load]);

  useEffect(() => {
    reload();
    return () => {
      loadSequence.current++;
    };
  }, [reload]);

  return useMemo(() => ({ items, loading, ready, reload }), [items, loading, ready, reload]);
}
