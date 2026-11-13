import { createContext, useContext, useState } from "react";

// Find in thread, on the transcript's side: which item the find bar has
// jumped to, so whatever is hiding it (a turn's fold, a run, a plan's steps,
// a long prompt's clamp) opens. Painting the occurrences is findPaint's, which
// loads with the bar rather than in the initial bundle.

/** The item the find bar last jumped to. A fresh object on every jump, so a
    second jump to the same item still counts as a new ask to show it. */
export type Reveal = { id: string };

const RevealContext = createContext<Reveal | null>(null);
export const FindRevealProvider = RevealContext.Provider;

/**
 * `useState(false)` for a disclosure that also opens itself whenever a find
 * jump lands on something it holds. It opens during the render the jump
 * arrives in, not an effect after it, so by the time the transcript's layout
 * effect looks for the item it is already in the DOM. Each jump opens it once:
 * the reader can close it again, and only the next jump reopens it.
 */
export function useRevealOpen(holds: (id: string) => boolean) {
  const reveal = useContext(RevealContext);
  const [open, setOpen] = useState(false);
  const [handled, setHandled] = useState<Reveal | null>(null);
  if (reveal && reveal !== handled && holds(reveal.id)) {
    setHandled(reveal);
    setOpen(true);
  }
  return [open, setOpen] as const;
}

/** Marks the element whose text find highlights inside an item's root, when
    the root carries more than the item's words (a footer, attachments). */
export const FIND_TEXT_ATTR = "data-find-text";
