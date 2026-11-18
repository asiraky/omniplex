import { describe, expect, it } from "vitest";

import { barKeyBytes, NO_MODS, withMods } from "./terminalKeys";

const ctrl = { ctrl: true, alt: false };
const alt = { ctrl: false, alt: true };
const both = { ctrl: true, alt: true };

describe("withMods", () => {
  it("turns a letter into its control code under ctrl, either case", () => {
    expect(withMods("c", ctrl)).toBe("\x03");
    expect(withMods("C", ctrl)).toBe("\x03");
    expect(withMods("a", ctrl)).toBe("\x01");
    expect(withMods("z", ctrl)).toBe("\x1a");
  });

  it("maps the punctuation with control codes", () => {
    expect(withMods("[", ctrl)).toBe("\x1b");
    expect(withMods("\\", ctrl)).toBe("\x1c");
    expect(withMods("]", ctrl)).toBe("\x1d");
    expect(withMods("^", ctrl)).toBe("\x1e");
    expect(withMods("_", ctrl)).toBe("\x1f");
    expect(withMods("@", ctrl)).toBe("\x00");
    expect(withMods(" ", ctrl)).toBe("\x00");
    expect(withMods("?", ctrl)).toBe("\x7f");
  });

  it("leaves a key with no control code, and more than one key, as typed", () => {
    expect(withMods("1", ctrl)).toBe("1");
    expect(withMods("ls", ctrl)).toBe("ls");
  });

  it("puts ESC in front under alt", () => {
    expect(withMods("x", alt)).toBe("\x1bx");
    expect(withMods("c", both)).toBe("\x1b\x03");
  });

  it("passes input through with nothing armed", () => {
    expect(withMods("c", NO_MODS)).toBe("c");
  });
});

describe("barKeyBytes", () => {
  it("sends esc and tab, with alt in front when armed", () => {
    expect(barKeyBytes("esc", NO_MODS, false)).toBe("\x1b");
    expect(barKeyBytes("tab", NO_MODS, false)).toBe("\t");
    expect(barKeyBytes("tab", alt, false)).toBe("\x1b\t");
    expect(barKeyBytes("tab", ctrl, false)).toBe("\t");
  });

  it("sends arrows in normal or application cursor mode", () => {
    expect(barKeyBytes("up", NO_MODS, false)).toBe("\x1b[A");
    expect(barKeyBytes("down", NO_MODS, false)).toBe("\x1b[B");
    expect(barKeyBytes("right", NO_MODS, false)).toBe("\x1b[C");
    expect(barKeyBytes("left", NO_MODS, false)).toBe("\x1b[D");
    expect(barKeyBytes("up", NO_MODS, true)).toBe("\x1bOA");
    expect(barKeyBytes("left", NO_MODS, true)).toBe("\x1bOD");
  });

  it("sends xterm's modified arrows, the same in either mode", () => {
    expect(barKeyBytes("up", ctrl, false)).toBe("\x1b[1;5A");
    expect(barKeyBytes("up", ctrl, true)).toBe("\x1b[1;5A");
    expect(barKeyBytes("left", alt, false)).toBe("\x1b[1;3D");
    expect(barKeyBytes("right", both, true)).toBe("\x1b[1;7C");
  });

  it("applies ctrl and alt to the shell characters", () => {
    expect(barKeyBytes("|", NO_MODS, false)).toBe("|");
    expect(barKeyBytes("/", ctrl, false)).toBe("\x1f");
    expect(barKeyBytes("~", alt, false)).toBe("\x1b~");
  });
});
