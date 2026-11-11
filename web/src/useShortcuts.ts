import { useEffect, useState } from "react";

import {
  commandLabel,
  currentPlatform,
  isElectronApp,
  shortcutsFor,
  type CommandId,
  type Platform,
  type Shortcut,
} from "~/lib/keybindings";
import { useCanHover, useIsDesktop } from "~/useMediaQuery";

/**
 * Whether to advertise shortcuts at all. A phone, or a tablet without a
 * pointer, shows none: the chords still work on a hardware keyboard, but a
 * hint for a key nobody can press is noise.
 */
export function useShowShortcuts(): boolean {
  const isDesktop = useIsDesktop();
  const canHover = useCanHover();
  return isDesktop && canHover;
}

/** "⇧⌘M" or "Ctrl+Shift+M" for a command, or null when it should not be shown here. */
export function useShortcutLabel(id: CommandId | undefined): string | null {
  const show = useShowShortcuts();
  if (!show || !id) return null;
  return commandLabel(id, isElectronApp(), currentPlatform());
}

/** The first chord for a command in this shell, for rendering as key chips. */
export function useShortcut(id: CommandId | undefined): { shortcut: Shortcut; platform: Platform } | null {
  const show = useShowShortcuts();
  if (!show || !id) return null;
  const shortcut = shortcutsFor(id, isElectronApp())[0];
  return shortcut ? { shortcut, platform: currentPlatform() } : null;
}

/** How long the modifier is held alone before the jump hints appear. */
export const MOD_HOLD_MS = 200;

/**
 * True while ⌘ (Ctrl elsewhere) is held on its own for a moment: the cue for
 * the ⌘1–9 chips on sidebar rows. Any other key, letting go, or leaving the
 * window clears it, so a quick ⌘C never flashes them.
 */
export function useModHeld(enabled: boolean): boolean {
  const [held, setHeld] = useState(false);

  useEffect(() => {
    if (!enabled) {
      setHeld(false);
      return;
    }
    const modKey = currentPlatform() === "mac" ? "Meta" : "Control";
    let timer: ReturnType<typeof setTimeout> | undefined;
    const clear = () => {
      clearTimeout(timer);
      timer = undefined;
      setHeld(false);
    };
    const onDown = (e: KeyboardEvent) => {
      if (e.key !== modKey || e.shiftKey || e.altKey) {
        clear();
        return;
      }
      if (e.repeat || timer !== undefined) return;
      timer = setTimeout(() => setHeld(true), MOD_HOLD_MS);
    };
    const onUp = (e: KeyboardEvent) => {
      if (e.key === modKey) clear();
    };
    window.addEventListener("keydown", onDown, true);
    window.addEventListener("keyup", onUp, true);
    window.addEventListener("blur", clear);
    return () => {
      clear();
      window.removeEventListener("keydown", onDown, true);
      window.removeEventListener("keyup", onUp, true);
      window.removeEventListener("blur", clear);
    };
  }, [enabled]);

  return held;
}
