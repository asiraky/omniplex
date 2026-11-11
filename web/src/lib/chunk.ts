export interface Chunk<T> {
  /** What the import gave, once it has arrived. */
  get(): T | undefined;
  /** Starts the import, or joins the one under way. Undefined if it failed. */
  load(): Promise<T | undefined>;
}

/**
 * A code-split import fetched once. A failed fetch is forgotten, so the next
 * `load` asks again: on a bad link the first try is often the one that drops.
 * Whether that refetches is the browser's call; Chrome hands back the same
 * failure until the page reloads.
 */
export function chunk<T>(importer: () => Promise<T>): Chunk<T> {
  let value: T | undefined;
  let loading: Promise<T | undefined> | undefined;
  return {
    get: () => value,
    load: () =>
      (loading ??= importer().then(
        (v) => (value = v),
        () => {
          loading = undefined;
          return undefined;
        },
      )),
  };
}
