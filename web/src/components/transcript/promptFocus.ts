import { createContext, useContext } from "react";

/** Which of the composer's blocking prompts a transcript record stands for. */
export type PromptKind = "permission" | "question";

/** What the composer's prompts expose, so a record can bring one into view. */
export interface PromptHandle {
  focus(): void;
}

/**
 * Brings the composer's live prompt into view and focuses it, from the
 * transcript's record of it. Provided by the thread view around the
 * transcript; absent (null) wherever there is no composer, which renders a
 * pending record as a plain label.
 */
export const PromptFocusContext = createContext<((kind: PromptKind) => void) | null>(null);

export function usePromptFocus() {
  return useContext(PromptFocusContext);
}

/**
 * Scrolls a prompt card into view and focuses `control`, else the card. Kept
 * synchronous: a phone opens its keyboard for a focus only inside the tap that
 * asked for it. The keyboard itself is the browser's to handle — the viewport
 * resizes the layout over it (`interactive-widget=resizes-content`), so the
 * dock, pinned to the bottom, rides up with it.
 */
export function focusPromptCard(card: HTMLElement | null, control: HTMLElement | null | undefined) {
  if (!card) return;
  card.scrollIntoView?.({ block: "nearest" });
  (control ?? card).focus();
}
