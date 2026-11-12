import { useEffect } from "react";

import { isComposing } from "~/lib/ime";
import {
  currentPlatform,
  isElectronApp,
  resolveCommand,
  type CommandId,
  type KeyContext,
} from "~/lib/keybindings";
import { useLatest } from "~/useLatest";

export type KeyHandlers = Partial<Record<CommandId, () => void>>;

/** The part of the context only the app knows; the rest is read off the DOM. */
export type AppKeyState = Pick<
  KeyContext,
  "paletteOpen" | "helpOpen" | "threadOpen" | "turnRunning" | "panelOpen"
>;

/**
 * Marks a dialog the shortcuts still reach as if it were not open: the phone's
 * sidebar sheet, which is a dialog to Radix but the app's own chrome to the
 * user. The palette and help overlay are dialogs like any other; the commands
 * that work over them say so with `inDialog`.
 */
export const KEYBINDINGS_PASS = { "data-keybindings": "pass" } as const;

// Anything that owns the keyboard while it is open. A listbox is here for a
// Select or the workspace list, which take typeahead and arrows. A layer
// playing its close animation is already gone to the user, so a chord pressed
// straight after Escape or a pick still lands.
const DIALOGS = [
  '[role="dialog"]:not([data-keybindings="pass"])',
  '[role="alertdialog"]',
  '[role="menu"]',
  '[role="listbox"]',
]
  .map((s) => `${s}:not([data-state="closed"])`)
  .join(", ");

function isEditable(el: Element | null): boolean {
  if (!el) return false;
  if (el instanceof HTMLElement && el.isContentEditable) return true;
  if (el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement) return true;
  if (el instanceof HTMLInputElement) {
    return !["button", "checkbox", "radio", "range", "reset", "submit", "color", "file"].includes(el.type);
  }
  return false;
}

/** Reads the moment's context: focus and open layers from the DOM, the rest from the app. */
export function readKeyContext(app: AppKeyState): KeyContext {
  const active = document.activeElement;
  return {
    ...app,
    electronApp: isElectronApp(),
    editableFocus: isEditable(active),
    composerFocus: !!active?.matches("[data-composer-input]"),
    dialogOpen: !!document.querySelector(DIALOGS),
    modelPickerOpen: !!document.querySelector('[aria-label="Harness and model"][aria-expanded="true"]'),
  };
}

/**
 * The one window listener for every shortcut. Chords with a modifier are
 * caught in the capture phase, ahead of whatever has focus, so ⌘B works from
 * the composer and the terminal alike. Escape and bare keys wait for the
 * bubble phase: a menu, popover or the composer's own slash menu gets them
 * first and claims them with preventDefault, the same rule Panel's Escape
 * follows.
 *
 * Mount it before App's early returns, so the setup, usage and tools screens
 * get the shortcuts that make sense there too.
 */
export function useKeybindings(handlers: KeyHandlers, app: AppKeyState) {
  const latest = useLatest({ handlers, app });

  useEffect(() => {
    const listen = (phase: "early" | "late") => (e: KeyboardEvent) => {
      if (isComposing(e) || e.repeat || e.defaultPrevented) return;
      const { handlers, app } = latest.current;
      const id = resolveCommand(e, readKeyContext(app), currentPlatform(), phase);
      const run = id && handlers[id];
      if (!run) return;
      e.preventDefault();
      e.stopPropagation();
      run();
    };
    const early = listen("early");
    const late = listen("late");
    window.addEventListener("keydown", early, true);
    window.addEventListener("keydown", late);
    return () => {
      window.removeEventListener("keydown", early, true);
      window.removeEventListener("keydown", late);
    };
  }, [latest]);
}
