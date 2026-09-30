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

const NO_ITEMS: ComposerItem[] = [];

/**
 * Fetches the catalogue on mount and whenever `load` changes: a new loader is
 * how the app says the provider's catalogue changed. Owned by whatever renders
 * the composer, keyed per thread there, so a thread switch starts empty. A
 * load that failed is tried again when a trigger opens (the composer calls
 * `reload`), so nothing here retries on its own.
 *
 * `scope` names what the catalogue is of, for an owner that is not remounted
 * when that changes: the draft composer, across its provider and folder
 * chips. A catalogue loaded under another scope is not shown under this one.
 */
export function useComposerItems(
  load: () => Promise<ComposerItem[]>,
  scope = "",
): ComposerCatalogue {
  const [loaded, setLoaded] = useState<{ scope: string; items: ComposerItem[] } | null>(null);
  const [loading, setLoading] = useState(false);
  const loadSequence = useRef(0);
  const ready = loaded?.scope === scope;
  const items = ready ? loaded.items : NO_ITEMS;

  const reload = useCallback(() => {
    const sequence = ++loadSequence.current;
    setLoading(true);
    load()
      .then((next) => {
        if (sequence === loadSequence.current) setLoaded({ scope, items: next });
      })
      .catch(() => {
        // Retain a previously successful catalogue. If the first request
        // failed, ready remains false and slash text is not sent as a prompt
        // while its behavior is unknown.
      })
      .finally(() => {
        if (sequence === loadSequence.current) setLoading(false);
      });
  }, [load, scope]);

  useEffect(() => {
    reload();
    return () => {
      loadSequence.current++;
    };
  }, [reload]);

  return useMemo(() => ({ items, loading, ready, reload }), [items, loading, ready, reload]);
}
