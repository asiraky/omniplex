import { cleanup, render as rtlRender } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, beforeEach, vi } from "vitest";

// Vitest is not running with `globals`, so Testing Library never installs its
// own teardown. Without this, every render in a file stacks up in one document
// and queries start matching the previous test's DOM.
afterEach(cleanup);

// jsdom ships no `matchMedia` at all, and several components read one on their
// first render. Desktop is the default so a test only has to say so when the
// size is what it is testing.
beforeEach(() => {
  viewport("desktop");
  // Radix's popper measures its anchor; jsdom has no ResizeObserver to measure
  // with. A no-op is enough: nothing here asserts on position.
  if (!("ResizeObserver" in window)) {
    vi.stubGlobal(
      "ResizeObserver",
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      },
    );
  }
});

import { ThemeProvider } from "~/components/ThemeProvider";
import { TooltipProvider } from "~/components/ui/tooltip";

/**
 * Renders inside the same providers `main.tsx` mounts. A component that needs
 * a theme or a tooltip context in the app needs one in a test too, and every
 * test wiring its own was three copies of the same tree.
 */
export function render(ui: ReactElement) {
  return rtlRender(wrap(ui));
}

/**
 * The same providers, for a re-render: Testing Library's `rerender` replaces
 * the whole tree it was given, so a test that re-renders has to hand back the
 * wrapper too or the second render loses its context.
 */
export function wrap(ui: ReactElement) {
  return (
    <ThemeProvider>
      <TooltipProvider>{ui}</TooltipProvider>
    </ThemeProvider>
  );
}

/**
 * Answer the `md` media query the way a phone or a desktop would.
 *
 * The layout reads this query in JS as well as in CSS — which shape the
 * sidebar takes, whether the new-session screen is a card or a page — so
 * "what size is the screen" is set here rather than by prop.
 */
export function viewport(kind: "phone" | "desktop") {
  const desktop = kind === "desktop";
  const listeners = new Set<(e: MediaQueryListEvent) => void>();
  vi.stubGlobal("matchMedia", (query: string) => ({
    // A desktop clears both width queries (md, and room to dock the panel);
    // anything else (a coarse pointer probe, reduced motion) answers false,
    // as jsdom would.
    matches: /min-width: (768|1024)px/.test(query) ? desktop : false,
    media: query,
    onchange: null,
    addEventListener: (_: string, cb: (e: MediaQueryListEvent) => void) => listeners.add(cb),
    removeEventListener: (_: string, cb: (e: MediaQueryListEvent) => void) => listeners.delete(cb),
    addListener: (cb: (e: MediaQueryListEvent) => void) => listeners.add(cb),
    removeListener: (cb: (e: MediaQueryListEvent) => void) => listeners.delete(cb),
    dispatchEvent: () => false,
  }));
}
