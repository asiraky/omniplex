import { createContext } from "react";

export type Draft = { text: string; base: number };
export type Kept = { draft: Draft | null; reason: string };

/**
 * Unsaved edits by agent and kind, held by the agent page so a trip to another
 * tab and back finds them. Without one an edit lives as long as the editor.
 */
export const RevisionDrafts = createContext<Map<string, Kept> | null>(null);
