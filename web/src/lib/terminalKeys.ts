// The bytes a phone's terminal key bar sends. A phone keyboard has no Esc,
// Tab, Ctrl, Alt or arrows, so the bar stands in for them, and its Ctrl and
// Alt also apply to the next key typed on the phone's own keyboard.

/** The bar's keys that send something, as against the modifiers it arms. */
export type BarKey = "esc" | "tab" | "left" | "up" | "down" | "right" | "|" | "~" | "/" | "-";

/** The sticky modifiers, armed by a tap and spent on the next key. */
export interface Mods {
  ctrl: boolean;
  alt: boolean;
}

export const NO_MODS: Mods = { ctrl: false, alt: false };

const ESC = "\x1b";

// What Ctrl makes of the keys that have a control code besides the letters,
// as xterm sends them. Ctrl+/ and Ctrl+- both send the code of Ctrl+_.
const CTRL_PUNCT: Record<string, string> = {
  "@": "\x00",
  " ": "\x00",
  "[": "\x1b",
  "\\": "\x1c",
  "]": "\x1d",
  "^": "\x1e",
  _: "\x1f",
  "/": "\x1f",
  "-": "\x1f",
  "?": "\x7f",
};

const ARROW_FINAL: Partial<Record<BarKey, string>> = { up: "A", down: "B", right: "C", left: "D" };

export function hasMods(mods: Mods) {
  return mods.ctrl || mods.alt;
}

/**
 * What the shell gets for input typed with the modifiers held. Ctrl turns a
 * single key with a control code into that code and leaves anything else as
 * typed; Alt is an ESC in front, as xterm sends it by default.
 */
export function withMods(data: string, mods: Mods): string {
  let out = data;
  if (mods.ctrl && [...data].length === 1) {
    if (/^[a-z]$/i.test(data)) out = String.fromCharCode(data.toUpperCase().charCodeAt(0) & 0x1f);
    else out = CTRL_PUNCT[data] ?? data;
  }
  return mods.alt ? ESC + out : out;
}

/**
 * The bytes for a tap on the bar. An arrow follows the cursor key mode the
 * program asked for (vim and less switch to application mode), unless a
 * modifier is held: xterm's modified arrows have one form in either mode.
 */
export function barKeyBytes(key: BarKey, mods: Mods, appCursor: boolean): string {
  const final = ARROW_FINAL[key];
  if (final) {
    if (hasMods(mods)) return `${ESC}[1;${1 + (mods.alt ? 2 : 0) + (mods.ctrl ? 4 : 0)}${final}`;
    return (appCursor ? `${ESC}O` : `${ESC}[`) + final;
  }
  return withMods(key === "esc" ? ESC : key === "tab" ? "\t" : key, mods);
}
