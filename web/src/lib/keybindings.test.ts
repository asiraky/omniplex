// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  commandLabel,
  currentPlatform,
  formatShortcut,
  jumpCommand,
  jumpIndex,
  matches,
  resolveCommand,
  shortcutsFor,
  type KeyContext,
  type KeyEventLike,
  type Platform,
  type Shortcut,
} from "./keybindings";

/** A key event as the browser would report it; modifiers default to up. */
function ev(key: string, code: string, mods: Partial<KeyEventLike> = {}): KeyEventLike {
  return { key, code, metaKey: false, ctrlKey: false, shiftKey: false, altKey: false, ...mods };
}

/** A chord pressed on a Mac, letters only. */
function press(s: Shortcut): KeyEventLike {
  return ev(s.key, `Key${s.key.toUpperCase()}`, { metaKey: !!s.mod, shiftKey: !!s.shift, altKey: !!s.alt });
}

const ctx = (over: Partial<KeyContext> = {}): KeyContext => ({
  electronApp: false,
  editableFocus: false,
  composerFocus: false,
  dialogOpen: false,
  paletteOpen: false,
  helpOpen: false,
  modelPickerOpen: false,
  threadOpen: true,
  turnRunning: false,
  panelOpen: false,
  ...over,
});

describe("matches", () => {
  it("matches a letter by the character typed", () => {
    expect(matches(ev("k", "KeyK", { ctrlKey: true }), { key: "k", mod: true }, "other")).toBe(true);
  });

  it("falls back to the physical key when the layout's letters are not Latin", () => {
    // Ctrl+K on a Russian layout types "л".
    expect(matches(ev("л", "KeyK", { ctrlKey: true }), { key: "k", mod: true }, "other")).toBe(true);
  });

  it("does not take the physical key over a Latin letter the layout typed", () => {
    // AZERTY: the key where QWERTY has Q types "a".
    expect(matches(ev("a", "KeyQ", { ctrlKey: true }), { key: "q", mod: true }, "other")).toBe(false);
    expect(matches(ev("a", "KeyQ", { ctrlKey: true }), { key: "a", mod: true }, "other")).toBe(true);
  });

  it("matches a Mac Option chord by position, though Option changes the character", () => {
    // ⌥⌘N types "˜".
    const e = ev("˜", "KeyN", { metaKey: true, altKey: true });
    expect(matches(e, { key: "n", mod: true, alt: true }, "mac")).toBe(true);
  });

  it("matches Shift+[ though it arrives as {", () => {
    const e = ev("{", "BracketLeft", { metaKey: true, shiftKey: true });
    expect(matches(e, { key: "[", mod: true, shift: true }, "mac")).toBe(true);
  });

  it("matches a digit by position on a layout where it needs Shift to type", () => {
    // AZERTY: the 1 key types "&" unshifted.
    expect(matches(ev("&", "Digit1", { ctrlKey: true }), { key: "1", mod: true }, "other")).toBe(true);
  });

  it("rejects AltGr typing a symbol on Windows and Linux", () => {
    // German AltGr+7 types "{"; Windows reports it as Ctrl+Alt.
    const e = ev("{", "Digit7", {
      ctrlKey: true,
      altKey: true,
      getModifierState: (k) => k === "AltGraph",
    });
    expect(matches(e, { key: "7", mod: true, alt: true }, "other")).toBe(false);
  });

  it("still matches Ctrl+Alt with a letter when AltGraph is reported", () => {
    const e = ev("b", "KeyB", { ctrlKey: true, altKey: true, getModifierState: (k) => k === "AltGraph" });
    expect(matches(e, { key: "b", mod: true, alt: true }, "other")).toBe(true);
  });

  it("requires the modifiers exactly", () => {
    const s = { key: "k", mod: true };
    expect(matches(ev("K", "KeyK", { ctrlKey: true, shiftKey: true }), s, "other")).toBe(false);
    expect(matches(ev("k", "KeyK", { ctrlKey: true, altKey: true }), s, "other")).toBe(false);
    expect(matches(ev("k", "KeyK"), s, "other")).toBe(false);
    // Shift is part of the chord, not optional.
    expect(matches(ev("[", "BracketLeft", { ctrlKey: true }), { key: "[", mod: true, shift: true }, "other")).toBe(
      false,
    );
  });

  it.each<[Platform, Partial<KeyEventLike>, Partial<KeyEventLike>]>([
    ["mac", { metaKey: true }, { ctrlKey: true }],
    ["other", { ctrlKey: true }, { metaKey: true }],
  ])("maps mod on %s and rejects the other key", (platform, right, wrong) => {
    const s = { key: "k", mod: true };
    expect(matches(ev("k", "KeyK", right), s, platform)).toBe(true);
    expect(matches(ev("k", "KeyK", wrong), s, platform)).toBe(false);
    expect(matches(ev("k", "KeyK", { metaKey: true, ctrlKey: true }), s, platform)).toBe(false);
  });

  it("matches a bare symbol by the character typed, Shift or not", () => {
    // US: Shift+/ is "?".
    expect(matches(ev("?", "Slash", { shiftKey: true }), { key: "?" }, "other")).toBe(true);
    expect(matches(ev("?", "Slash", { shiftKey: true }), { key: "/" }, "other")).toBe(false);
    // German: "/" is Shift+7.
    expect(matches(ev("/", "Digit7", { shiftKey: true }), { key: "/" }, "other")).toBe(true);
    // A modifier makes it a different chord.
    expect(matches(ev("/", "Slash", { ctrlKey: true }), { key: "/" }, "other")).toBe(false);
    expect(matches(ev("/", "Slash", { metaKey: true }), { key: "/" }, "mac")).toBe(false);
  });

  it("treats Esc as Escape", () => {
    expect(matches(ev("Esc", "Escape"), { key: "escape" }, "other")).toBe(true);
  });
});

describe("resolveCommand", () => {
  const early = (e: KeyEventLike, c: KeyContext, platform: Platform = "mac") =>
    resolveCommand(e, c, platform, "early");
  const late = (e: KeyEventLike, c: KeyContext, platform: Platform = "mac") =>
    resolveCommand(e, c, platform, "late");

  const app = ctx({ electronApp: true });
  const browser = ctx({ electronApp: false });

  it("answers each shell's own chord for New thread in…, and not the other's", () => {
    const [inApp] = shortcutsFor("newThreadIn", true);
    const [inBrowser] = shortcutsFor("newThreadIn", false);
    expect(inApp).not.toEqual(inBrowser);
    expect(early(press(inApp!), app)).toBe("newThreadIn");
    expect(early(press(inApp!), browser)).not.toBe("newThreadIn");
    expect(early(press(inBrowser!), browser)).toBe("newThreadIn");
    expect(early(press(inBrowser!), app)).not.toBe("newThreadIn");
  });

  it("jumps by number and closes the panel with ⌘W only in the app", () => {
    for (let n = 1; n <= 9; n++) {
      const e = ev(String(n), `Digit${n}`, { metaKey: true });
      expect(early(e, app)).toBe(`jumpThread${n}`);
      expect(early(e, browser)).toBeNull();
    }
    const w = ev("w", "KeyW", { metaKey: true });
    expect(early(w, app)).toBe("closePanel");
    expect(early(w, browser)).toBeNull();
  });

  it("uses Ctrl for the same chords off a Mac", () => {
    expect(early(ev("b", "KeyB", { ctrlKey: true }), browser, "other")).toBe("toggleSidebar");
    expect(early(ev("b", "KeyB", { metaKey: true }), browser, "other")).toBeNull();
    expect(early(ev("b", "KeyB", { ctrlKey: true, altKey: true }), browser, "other")).toBe("togglePanel");
  });

  it("stops a turn with ⌘. only while one runs", () => {
    const e = ev(".", "Period", { metaKey: true });
    expect(early(e, ctx({ turnRunning: true }))).toBe("stopTurn");
    expect(early(e, ctx({ turnRunning: false }))).toBeNull();
  });

  it("stops a turn with Escape from the composer, late, with nothing over it", () => {
    const esc = ev("Escape", "Escape");
    const ready = ctx({ turnRunning: true, composerFocus: true, editableFocus: true });
    expect(late(esc, ready)).toBe("stopTurnEscape");
    // The composer's own handlers and any open layer get Escape first.
    expect(early(esc, ready)).toBeNull();
    expect(late(esc, { ...ready, composerFocus: false })).toBeNull();
    expect(late(esc, { ...ready, turnRunning: false })).toBeNull();
    expect(late(esc, { ...ready, dialogOpen: true })).toBeNull();
  });

  it("asks the thread-only commands for an open thread", () => {
    const diff = ev("D", "KeyD", { metaKey: true, shiftKey: true });
    expect(early(diff, ctx({ threadOpen: true }))).toBe("openDiff");
    expect(early(diff, ctx({ threadOpen: false }))).toBeNull();
  });

  it("blocks commands under a dialog", () => {
    const open = ctx({ dialogOpen: true });
    expect(early(ev("b", "KeyB", { metaKey: true }), open)).toBeNull();
    expect(early(ev(",", "Comma", { metaKey: true }), open)).toBeNull();
    expect(early(ev("k", "KeyK", { metaKey: true }), open)).toBeNull();
  });

  it("lets ⌘K through the palette and the help overlay", () => {
    const k = ev("k", "KeyK", { metaKey: true });
    expect(early(k, ctx({ dialogOpen: true, paletteOpen: true }))).toBe("palette");
    expect(early(k, ctx({ dialogOpen: true, helpOpen: true }))).toBe("palette");
  });

  it("lets the model and effort chords through the open model picker only", () => {
    const m = ev("M", "KeyM", { metaKey: true, shiftKey: true });
    const e = ev("E", "KeyE", { metaKey: true, shiftKey: true });
    const picker = ctx({ dialogOpen: true, modelPickerOpen: true });
    expect(early(m, picker)).toBe("modelPicker");
    expect(early(e, picker)).toBe("effort");
    expect(early(m, ctx({ dialogOpen: true }))).toBeNull();
  });

  it("lets New thread in… through the palette but not another dialog", () => {
    const n = ev("n", "KeyN", { metaKey: true });
    expect(early(n, { ...app, dialogOpen: true, paletteOpen: true })).toBe("newThreadIn");
    expect(early(n, { ...app, dialogOpen: true, helpOpen: true })).toBeNull();
  });

  it("lets the help chord close the help overlay", () => {
    const slash = ev("/", "Slash", { metaKey: true });
    expect(early(slash, ctx({ dialogOpen: true, helpOpen: true }))).toBe("shortcutsHelp");
    expect(early(slash, ctx({ dialogOpen: true, paletteOpen: true }))).toBeNull();
  });

  it("never fires a bare key in a text field, but a mod chord does", () => {
    const typing = ctx({ editableFocus: true });
    expect(late(ev("/", "Slash"), typing)).toBeNull();
    expect(early(ev("?", "Slash", { shiftKey: true }), typing)).toBeNull();
    expect(early(ev("/", "Slash", { metaKey: true }), typing)).toBe("shortcutsHelp");
    expect(early(ev("b", "KeyB", { metaKey: true }), typing)).toBe("toggleSidebar");
  });

  it("fires the bare keys outside a field", () => {
    expect(late(ev("/", "Slash"), ctx())).toBe("focusComposer");
    expect(early(ev("?", "Slash", { shiftKey: true }), ctx())).toBe("shortcutsHelp");
  });

  it("keeps each command to its phase", () => {
    expect(early(ev("/", "Slash"), ctx())).toBeNull();
    expect(late(ev("b", "KeyB", { metaKey: true }), ctx())).toBeNull();
  });

  it("matches nothing for a plain letter", () => {
    expect(early(ev("k", "KeyK"), ctx())).toBeNull();
    expect(late(ev("k", "KeyK"), ctx())).toBeNull();
  });
});

describe("labels", () => {
  it.each<[Parameters<typeof formatShortcut>[0], string, string]>([
    [{ key: "m", mod: true, shift: true }, "⇧⌘M", "Ctrl+Shift+M"],
    [{ key: "b", mod: true, alt: true }, "⌥⌘B", "Ctrl+Alt+B"],
    [{ key: "?" }, "?", "?"],
    [{ key: "arrowdown", alt: true, shift: true }, "⌥⇧↓", "Alt+Shift+↓"],
    [{ key: "[", mod: true, shift: true }, "⇧⌘[", "Ctrl+Shift+["],
    [{ key: "escape" }, "Esc", "Esc"],
  ])("writes %o as %s on a Mac and %s elsewhere", (s, mac, other) => {
    expect(formatShortcut(s, "mac")).toBe(mac);
    expect(formatShortcut(s, "other")).toBe(other);
  });

  it("labels a command by its first chord in this shell", () => {
    for (const electronApp of [true, false]) {
      for (const platform of ["mac", "other"] as const) {
        const [first] = shortcutsFor("newThreadIn", electronApp);
        expect(commandLabel("newThreadIn", electronApp, platform)).toBe(formatShortcut(first!, platform));
      }
    }
    expect(commandLabel("newThreadIn", true, "mac")).not.toBe(commandLabel("newThreadIn", false, "mac"));
  });

  it("has no label for an app-only command in a browser", () => {
    expect(commandLabel("closePanel", false, "mac")).toBeNull();
    expect(commandLabel("jumpThread1", false, "mac")).toBeNull();
    expect(commandLabel("jumpThread1", true, "mac")).toBe("⌘1");
  });
});

describe("currentPlatform", () => {
  afterEach(() => {
    delete window.omniplexDesktop;
    vi.unstubAllGlobals();
  });

  const nav = (n: { platform?: string; userAgentData?: { platform?: string } }) =>
    vi.stubGlobal("navigator", { platform: "", ...n });

  it("believes the desktop app's own report first", () => {
    nav({ platform: "Win32" });
    window.omniplexDesktop = { platform: "darwin" };
    expect(currentPlatform()).toBe("mac");
    window.omniplexDesktop = { platform: "linux" };
    nav({ platform: "MacIntel" });
    expect(currentPlatform()).toBe("other");
  });

  it("reads userAgentData ahead of navigator.platform", () => {
    nav({ userAgentData: { platform: "macOS" }, platform: "Linux x86_64" });
    expect(currentPlatform()).toBe("mac");
    nav({ userAgentData: { platform: "Windows" }, platform: "MacIntel" });
    expect(currentPlatform()).toBe("other");
  });

  it.each([
    ["MacIntel", "mac"],
    ["iPad", "mac"],
    ["Win32", "other"],
    ["Linux x86_64", "other"],
    ["", "other"],
  ])("reads navigator.platform %j as %s", (platform, want) => {
    nav({ platform, userAgentData: { platform: "" } });
    expect(currentPlatform()).toBe(want);
  });
});

describe("jump commands", () => {
  it("round-trips the nine sidebar positions", () => {
    for (let i = 0; i < 9; i++) {
      const id = jumpCommand(i);
      expect(id).not.toBeNull();
      expect(jumpIndex(id!)).toBe(i);
    }
  });

  it("has no command past the ninth row or before the first", () => {
    expect(jumpCommand(9)).toBeNull();
    expect(jumpCommand(-1)).toBeNull();
  });

  it("gives no index for other commands", () => {
    expect(jumpIndex("palette")).toBeNull();
  });
});
