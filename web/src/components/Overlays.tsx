import { lazy, Suspense, useEffect, useState } from "react";

import type { CommandPaletteProps } from "./CommandPalette";

// The palette and the shortcuts list stay out of the first load, like the
// dialogs in AppDialogs. Each mounts the first time it opens and then stays
// mounted, so it can animate closed; both are fetched once the page is idle,
// so the first ⌘K does not wait on a round trip.
const loadPalette = () => import("./CommandPalette");
const loadHelp = () => import("./ShortcutsHelp");
const LazyPalette = lazy(() => loadPalette().then((m) => ({ default: m.CommandPalette })));
const LazyHelp = lazy(() => loadHelp().then((m) => ({ default: m.ShortcutsHelp })));

function usePrefetch() {
  useEffect(() => {
    const prefetch = () => {
      void loadPalette();
      void loadHelp();
    };
    if (typeof requestIdleCallback === "function") {
      const id = requestIdleCallback(prefetch, { timeout: 5000 });
      return () => cancelIdleCallback(id);
    }
    const id = setTimeout(prefetch, 2000);
    return () => clearTimeout(id);
  }, []);
}

/** True from the first render where `open` is, onwards. */
function useOpenedOnce(open: boolean) {
  const [opened, setOpened] = useState(open);
  if (open && !opened) setOpened(true);
  return opened;
}

export function CommandPalette(props: CommandPaletteProps) {
  usePrefetch();
  if (!useOpenedOnce(props.open)) return null;
  return (
    <Suspense fallback={null}>
      <LazyPalette {...props} />
    </Suspense>
  );
}

export function ShortcutsHelp(props: { open: boolean; onOpenChange: (open: boolean) => void }) {
  if (!useOpenedOnce(props.open)) return null;
  return (
    <Suspense fallback={null}>
      <LazyHelp {...props} />
    </Suspense>
  );
}
