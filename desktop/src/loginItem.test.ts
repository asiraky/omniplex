import { describe, expect, it } from "vitest";
import { launchedHidden } from "./loginItem";

describe("launchedHidden", () => {
  it("recognises the Windows login entry by its argument", () => {
    expect(launchedHidden({ platform: "win32", argv: ["Omniplex.exe", "--hidden"], wasOpenedAtLogin: false })).toBe(true);
    expect(launchedHidden({ platform: "win32", argv: ["Omniplex.exe"], wasOpenedAtLogin: true })).toBe(false);
  });

  it("trusts macOS about a launch at login", () => {
    expect(launchedHidden({ platform: "darwin", argv: ["Omniplex"], wasOpenedAtLogin: true })).toBe(true);
    expect(launchedHidden({ platform: "darwin", argv: ["Omniplex"], wasOpenedAtLogin: false })).toBe(false);
  });
});
