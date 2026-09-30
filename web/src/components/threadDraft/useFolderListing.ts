import { useEffect, useState } from "react";

/**
 * Something the server lists for one git folder, fetched fresh whenever the
 * folder changes. Nothing is listed without a folder.
 *
 * The answer is kept with the folder it came from, so a list for another
 * folder is never shown, not even for the render before the new one is asked
 * for. `empty` and `failed` should be defined once, outside the component:
 * a new `failed` every render would ask again every render.
 */
export function useFolderListing<T>(
  load: (projectId: string, folderId: string) => Promise<T>,
  projectId: string | undefined,
  folderId: string | undefined,
  empty: T,
  failed: (e: unknown) => T,
) {
  const key = projectId && folderId ? JSON.stringify([projectId, folderId]) : "";
  const [result, setResult] = useState<{ key: string; value: T } | null>(null);
  // Leaving a folder forgets its answer, so coming back to it asks again
  // rather than offering what was there before.
  if (result && result.key !== key) setResult(null);

  useEffect(() => {
    if (!projectId || !folderId) return;
    const key = JSON.stringify([projectId, folderId]);
    let live = true;
    load(projectId, folderId)
      .catch(failed)
      .then((value) => live && setResult({ key, value }));
    return () => {
      live = false;
    };
  }, [projectId, folderId, load, failed]);

  const current = result?.key === key ? result : null;
  return { value: current ? current.value : empty, loading: !!key && !current };
}
