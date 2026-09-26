import { useEffect, useState } from "react";

// Tailwind's `md` breakpoint. Layout is CSS-driven wherever possible, but a
// few decisions genuinely need JS — whether the sidebar starts open, whether
// to advertise a keyboard shortcut — and those must agree with the CSS.
const DESKTOP = "(min-width: 768px)";
// A pointer that cannot hover. Not the same question as screen size: a tablet
// is wide and touch-only, a small window on a laptop is narrow and has a mouse.
const COARSE = "(pointer: coarse)";

// Room for the transcript and a docked side panel both: below this the panel
// is a full-screen sheet. A phone on its side clears `md` but not this.
const DOCKS_PANEL = "(min-width: 1024px)";

function useMatches(query: string): boolean {
  const [matches, setMatches] = useState(() => window.matchMedia(query).matches);

  useEffect(() => {
    const mq = window.matchMedia(query);
    setMatches(mq.matches);
    const onChange = (e: MediaQueryListEvent) => setMatches(e.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [query]);

  return matches;
}

export function useIsDesktop(): boolean {
  return useMatches(DESKTOP);
}

export function useDocksPanel(): boolean {
  return useMatches(DOCKS_PANEL);
}

export function useIsCoarsePointer(): boolean {
  return useMatches(COARSE);
}
