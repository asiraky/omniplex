// Keyboard shortcuts: the one table of commands, how a key event matches one,
// and how a chord is written for the platform it runs on.
//
// Defaults live here and nowhere else. Rebinding waits until someone wants it,
// and when it comes it belongs in the server's UserConfig so it follows the
// user between devices; nothing below assumes the table is constant.
//
// Matching is ported from T3 Code's keybindingMatching.ts: letters match by the
// character typed, falling back to the physical key so a Cyrillic or Greek
// layout still reaches Ctrl+K, and punctuation matches by physical key so
// Shift+[ is `[` and not `{`. AltGr (Ctrl+Alt on Windows and Linux) typing a
// symbol is never a shortcut.

export type Platform = "mac" | "other";

/** One chord. `mod` is ⌘ on a Mac and Ctrl everywhere else. */
export interface Shortcut {
  /** Lower-case `KeyboardEvent.key`, or the unshifted character for punctuation. */
  key: string;
  mod?: boolean;
  shift?: boolean;
  alt?: boolean;
}

/** What the dispatcher knows about the moment a key was pressed. */
export interface KeyContext {
  /** Running inside the Omniplex desktop app rather than a browser tab. */
  electronApp: boolean;
  /** Focus is in a text field, select or contenteditable. */
  editableFocus: boolean;
  /** Focus is in a composer's text field. */
  composerFocus: boolean;
  /**
   * A dialog, sheet, popover, menu or listbox is open, the palette and help
   * overlay included: something that owns the keyboard, Escape first.
   */
  dialogOpen: boolean;
  paletteOpen: boolean;
  helpOpen: boolean;
  modelPickerOpen: boolean;
  /** A thread is open, rather than a draft or the empty state. */
  threadOpen: boolean;
  turnRunning: boolean;
  panelOpen: boolean;
}

export type CommandGroup = "Threads" | "Composer" | "Panels" | "App";

export interface CommandSpec {
  label: string;
  group: CommandGroup;
  /** Chords in the desktop app. */
  app: Shortcut[];
  /** Chords in a browser tab, where the browser keeps Ctrl/⌘+N, T, W and 1–9. */
  browser: Shortcut[];
  /** Whether the command can fire now. Absent means always. */
  guard?: (ctx: KeyContext) => boolean;
  /**
   * Whether it still fires with a dialog open. Absent means it does not: a
   * shortcut firing under a confirm dialog acts on something the user cannot
   * see.
   */
  inDialog?: (ctx: KeyContext) => boolean;
  /**
   * Fires in the bubble phase, after the focused element and any open layer
   * have had the key. Used for Escape and bare keys, which those layers own
   * first and claim with preventDefault.
   */
  late?: boolean;
  /** Left out of the help overlay; the jump commands are listed as one row. */
  hidden?: boolean;
  /**
   * Listed in the help overlay on another command's row, as one more way to
   * it. A command id; typed loosely because COMMANDS is what defines the ids.
   */
  listWith?: string;
}

const mod = (key: string, extra: Omit<Shortcut, "key" | "mod"> = {}): Shortcut => ({
  key,
  mod: true,
  ...extra,
});

const inPalette = (ctx: KeyContext) => ctx.paletteOpen;
// The help overlay's footer points at the palette, so ⌘K works from it.
const inPaletteOrHelp = (ctx: KeyContext) => ctx.paletteOpen || ctx.helpOpen;

const JUMPS = [1, 2, 3, 4, 5, 6, 7, 8, 9] as const;
type JumpId = `jumpThread${(typeof JUMPS)[number]}`;

const jumpCommands = Object.fromEntries(
  JUMPS.map((n): [JumpId, CommandSpec] => [
    `jumpThread${n}`,
    {
      label: `Go to thread ${n}`,
      group: "Threads",
      app: [mod(String(n))],
      browser: [],
      hidden: true,
    },
  ]),
) as Record<JumpId, CommandSpec>;

export const COMMANDS = {
  palette: {
    label: "Command palette",
    group: "App",
    app: [mod("k")],
    browser: [mod("k")],
    inDialog: inPaletteOrHelp,
  },
  newThreadIn: {
    label: "New thread in…",
    group: "Threads",
    app: [mod("n")],
    // Ctrl/⌘+N belongs to the browser.
    browser: [mod("o", { shift: true })],
    inDialog: inPalette,
  },
  newThreadHere: {
    label: "New thread in this project",
    group: "Threads",
    app: [mod("n", { shift: true })],
    browser: [mod("n", { alt: true })],
    inDialog: inPalette,
  },
  prevThread: {
    label: "Previous thread",
    group: "Threads",
    app: [mod("[", { shift: true })],
    browser: [mod("[", { shift: true })],
  },
  nextThread: {
    label: "Next thread",
    group: "Threads",
    app: [mod("]", { shift: true })],
    browser: [mod("]", { shift: true })],
  },
  ...jumpCommands,
  nextAttention: {
    label: "Next thread that needs you",
    group: "Threads",
    app: [{ key: "arrowdown", alt: true, shift: true }],
    browser: [{ key: "arrowdown", alt: true, shift: true }],
  },
  copyTranscript: {
    label: "Copy transcript",
    group: "Threads",
    app: [mod("c", { shift: true })],
    browser: [mod("c", { shift: true })],
    guard: (ctx) => ctx.threadOpen,
  },
  stopTurn: {
    label: "Stop the turn",
    group: "Composer",
    app: [mod(".")],
    browser: [mod(".")],
    guard: (ctx) => ctx.turnRunning,
  },
  stopTurnEscape: {
    label: "Stop the turn",
    group: "Composer",
    app: [{ key: "escape" }],
    browser: [{ key: "escape" }],
    // No inDialog: a menu or popover over the composer owns Escape.
    guard: (ctx) => ctx.turnRunning && ctx.composerFocus,
    late: true,
    listWith: "stopTurn",
  },
  focusComposer: {
    label: "Focus the composer",
    group: "Composer",
    app: [{ key: "/" }],
    browser: [{ key: "/" }],
    late: true,
  },
  modelPicker: {
    label: "Model picker",
    group: "Composer",
    app: [mod("m", { shift: true })],
    browser: [mod("m", { shift: true })],
    inDialog: (ctx) => ctx.modelPickerOpen,
  },
  effort: {
    label: "Reasoning effort",
    group: "Composer",
    app: [mod("e", { shift: true })],
    browser: [mod("e", { shift: true })],
    inDialog: (ctx) => ctx.modelPickerOpen,
  },
  toggleSidebar: {
    label: "Toggle threads",
    group: "Panels",
    app: [mod("b")],
    browser: [mod("b")],
  },
  togglePanel: {
    label: "Toggle side panel",
    group: "Panels",
    app: [mod("b", { alt: true })],
    browser: [mod("b", { alt: true })],
    guard: (ctx) => ctx.threadOpen,
  },
  openDiff: {
    label: "Open diff",
    group: "Panels",
    app: [mod("d", { shift: true })],
    browser: [mod("d", { shift: true })],
    guard: (ctx) => ctx.threadOpen,
  },
  closePanel: {
    // The desktop app owns ⌘W. Closing the window would lose an unsent draft's
    // choices, so it closes the side panel when one is open and otherwise does
    // nothing. The dispatcher claims it either way.
    label: "Close side panel",
    group: "Panels",
    app: [mod("w")],
    browser: [],
  },
  settings: {
    label: "Settings",
    group: "App",
    app: [mod(",")],
    browser: [mod(",")],
  },
  shortcutsHelp: {
    label: "Keyboard shortcuts",
    group: "App",
    app: [mod("/"), { key: "?" }],
    browser: [mod("/"), { key: "?" }],
    inDialog: (ctx) => ctx.helpOpen,
  },
} satisfies Record<string, CommandSpec>;

export type CommandId = keyof typeof COMMANDS;

export const COMMAND_IDS = Object.keys(COMMANDS) as CommandId[];

/** The jump command for the thread at `index` (0-based) in sidebar order, if any. */
export function jumpCommand(index: number): CommandId | null {
  return index >= 0 && index < JUMPS.length ? (`jumpThread${index + 1}` as CommandId) : null;
}

/** The 0-based sidebar index a jump command targets, or null for any other command. */
export function jumpIndex(id: CommandId): number | null {
  const m = /^jumpThread(\d)$/.exec(id);
  return m ? Number(m[1]) - 1 : null;
}

// ---- platform ---------------------------------------------------------------

interface DesktopBridge {
  platform: string;
}

declare global {
  interface Window {
    /** Set by the desktop app's preload; absent in a browser. */
    omniplexDesktop?: DesktopBridge;
  }
  interface Navigator {
    userAgentData?: { platform?: string };
  }
}

export function isMacPlatform(platform: string): boolean {
  return /mac|darwin|iphone|ipad|ipod/i.test(platform);
}

/** "mac" when ⌘ is the command key here, from the most specific source going. */
export function currentPlatform(): Platform {
  const raw =
    window.omniplexDesktop?.platform ||
    navigator.userAgentData?.platform ||
    navigator.platform ||
    "";
  return isMacPlatform(raw) ? "mac" : "other";
}

export function isElectronApp(): boolean {
  return !!window.omniplexDesktop;
}

/** The chords a command answers to in this shell. */
export function shortcutsFor(id: CommandId, electronApp: boolean): Shortcut[] {
  const spec: CommandSpec = COMMANDS[id];
  return electronApp ? spec.app : spec.browser;
}

// ---- matching ---------------------------------------------------------------

export interface KeyEventLike {
  key: string;
  code?: string;
  metaKey: boolean;
  ctrlKey: boolean;
  shiftKey: boolean;
  altKey: boolean;
  getModifierState?: (key: string) => boolean;
}

const CODE_KEYS: Readonly<Record<string, string>> = {
  Backquote: "`",
  Backslash: "\\",
  BracketLeft: "[",
  BracketRight: "]",
  Comma: ",",
  Digit0: "0",
  Digit1: "1",
  Digit2: "2",
  Digit3: "3",
  Digit4: "4",
  Digit5: "5",
  Digit6: "6",
  Digit7: "7",
  Digit8: "8",
  Digit9: "9",
  Equal: "=",
  Minus: "-",
  Period: ".",
  Quote: "'",
  Semicolon: ";",
  Slash: "/",
};

function normalizeKey(key: string): string {
  const k = key.toLowerCase();
  return k === "esc" ? "escape" : k;
}

/** Every name the pressed key could go by: what it typed, and where it sits. */
export function eventKeys(e: KeyEventLike): Set<string> {
  const typed = normalizeKey(e.key);
  const keys = new Set([typed]);
  // Letters by position, for layouts whose letters are not Latin. On a Mac,
  // Option+N types "˜", so this is also what makes ⌥ chords match.
  const letter = e.code?.match(/^Key([A-Z])$/)?.[1];
  if (letter && !/^[a-z]$/.test(typed)) keys.add(letter.toLowerCase());
  // Punctuation and digits by position: Shift+[ types "{", Shift+1 types "!".
  const physical = e.code ? CODE_KEYS[e.code] : undefined;
  if (physical && !/^[a-z]$/.test(typed)) keys.add(physical);
  return keys;
}

function bareSymbol(s: Shortcut): boolean {
  return !s.mod && !s.alt && s.key.length === 1 && !/^[a-z0-9]$/.test(s.key);
}

export function matches(e: KeyEventLike, s: Shortcut, platform: Platform): boolean {
  // AltGr reports as Ctrl+Alt on Windows and Linux; when it types a symbol
  // it is someone typing, not a chord.
  if (platform !== "mac" && e.getModifierState?.("AltGraph") && !/^[a-z0-9]$/i.test(e.key)) {
    return false;
  }
  // A bare symbol is the character typed, wherever the layout keeps it and
  // whatever it takes to reach: "/" is Shift+7 on a German keyboard and "?"
  // is Shift+, on a French one.
  if (bareSymbol(s)) {
    return !e.metaKey && !e.ctrlKey && !e.altKey && normalizeKey(e.key) === s.key;
  }
  const mac = platform === "mac";
  const wantMeta = !!s.mod && mac;
  const wantCtrl = !!s.mod && !mac;
  if (e.metaKey !== wantMeta || e.ctrlKey !== wantCtrl) return false;
  if (e.altKey !== !!s.alt || e.shiftKey !== !!s.shift) return false;
  return eventKeys(e).has(s.key);
}

/** A chord that types a character: in a text field it is typing, never a shortcut. */
function typesCharacter(s: Shortcut): boolean {
  return !s.mod && !s.alt && s.key.length === 1;
}

/** The first command that matches the event and may fire now, or null. */
export function resolveCommand(
  e: KeyEventLike,
  ctx: KeyContext,
  platform: Platform,
  phase: "early" | "late",
): CommandId | null {
  for (const id of COMMAND_IDS) {
    const spec: CommandSpec = COMMANDS[id];
    if (!!spec.late !== (phase === "late")) continue;
    const hit = shortcutsFor(id, ctx.electronApp).find((s) => matches(e, s, platform));
    if (!hit) continue;
    if (ctx.editableFocus && typesCharacter(hit)) continue;
    if (spec.guard && !spec.guard(ctx)) continue;
    if (ctx.dialogOpen && !spec.inDialog?.(ctx)) continue;
    return id;
  }
  return null;
}

// ---- labels -----------------------------------------------------------------

const KEY_LABELS: Record<string, string> = {
  arrowup: "↑",
  arrowdown: "↓",
  arrowleft: "←",
  arrowright: "→",
  escape: "Esc",
  enter: "↵",
  " ": "Space",
};

function keyLabel(key: string): string {
  return KEY_LABELS[key] ?? (key.length === 1 ? key.toUpperCase() : key[0].toUpperCase() + key.slice(1));
}

/** A shifted symbol is written as itself: "?", not "Shift+?". */
function shiftShown(s: Shortcut): boolean {
  return !!s.shift && /^[a-z0-9[\]\\;',./`=-]$|^arrow|^escape$|^enter$/.test(s.key);
}

/** The chord's parts, in platform order: ["⇧", "⌘", "M"] or ["Ctrl", "Shift", "M"]. */
export function shortcutParts(s: Shortcut, platform: Platform): string[] {
  const parts: string[] = [];
  const shift = shiftShown(s);
  if (platform === "mac") {
    if (s.alt) parts.push("⌥");
    if (shift) parts.push("⇧");
    if (s.mod) parts.push("⌘");
  } else {
    if (s.mod) parts.push("Ctrl");
    if (s.alt) parts.push("Alt");
    if (shift) parts.push("Shift");
  }
  parts.push(keyLabel(s.key));
  return parts;
}

/** "⇧⌘M" on a Mac, "Ctrl+Shift+M" elsewhere. */
export function formatShortcut(s: Shortcut, platform: Platform): string {
  return shortcutParts(s, platform).join(platform === "mac" ? "" : "+");
}

/** The label for a command's first chord in this shell, or null when it has none here. */
export function commandLabel(id: CommandId, electronApp: boolean, platform: Platform): string | null {
  const s = shortcutsFor(id, electronApp)[0];
  return s ? formatShortcut(s, platform) : null;
}
