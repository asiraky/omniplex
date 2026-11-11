import type { MenuItemConstructorOptions } from "electron";
import { describe, expect, it } from "vitest";
import { appMenuTemplate } from "./menu";

const PLATFORMS = ["darwin", "win32", "linux"] as const satisfies readonly NodeJS.Platform[];

// Electron's default accelerators for the roles a menu could use, from
// lib/browser/api/menu-item-roles.ts. A role missing here fails the test, so
// adding one to the menu means looking its accelerator up first. Composite
// roles (fileMenu, viewMenu, windowMenu, ...) expand to the items they bring.
function roleChords(role: string, platform: NodeJS.Platform): string[] {
  const mac = platform === "darwin";
  const win = platform === "win32";
  const single: Record<string, string | undefined> = {
    about: undefined,
    close: "CommandOrControl+W",
    copy: "CommandOrControl+C",
    cut: "CommandOrControl+X",
    delete: undefined,
    forceReload: "Shift+CommandOrControl+R",
    front: undefined,
    hide: "Command+H",
    hideOthers: mac ? "Command+Alt+H" : "Control+Shift+H",
    minimize: "CommandOrControl+M",
    paste: "CommandOrControl+V",
    pasteAndMatchStyle: mac ? "Cmd+Option+Shift+V" : "Shift+CommandOrControl+V",
    quit: win ? undefined : "CommandOrControl+Q",
    redo: win ? "Control+Y" : "Shift+CommandOrControl+Z",
    reload: "CmdOrCtrl+R",
    resetZoom: "CommandOrControl+0",
    selectAll: "CommandOrControl+A",
    toggleDevTools: mac ? "Alt+Command+I" : "Ctrl+Shift+I",
    togglefullscreen: mac ? "Control+Command+F" : "F11",
    undo: "CommandOrControl+Z",
    unhide: undefined,
    window: undefined,
    zoom: undefined,
    zoomIn: "CommandOrControl+Plus",
    zoomOut: "CommandOrControl+-",
  };
  const composite: Record<string, string[]> = {
    appMenu: ["about", "hide", "hideOthers", "unhide", "quit"],
    fileMenu: [mac ? "close" : "quit"],
    editMenu: ["undo", "redo", "cut", "copy", "paste", "pasteAndMatchStyle", "delete", "selectAll"],
    viewMenu: ["reload", "forceReload", "toggleDevTools", "resetZoom", "zoomIn", "zoomOut", "togglefullscreen"],
    windowMenu: ["minimize", "zoom", mac ? "front" : "close"],
  };
  if (role in composite) return composite[role]!.flatMap((r) => roleChords(r, platform));
  if (!(role in single)) throw new Error(`no default accelerator known for role ${role}`);
  const chord = single[role];
  return chord ? [normalise(chord, platform)] : [];
}

// "Shift+CmdOrCtrl+R" on Windows -> "Ctrl+Shift+R": one spelling per chord.
function normalise(accelerator: string, platform: NodeJS.Platform): string {
  const parts = accelerator.split("+");
  const key = parts.pop()!.toUpperCase();
  const mods = new Set(
    parts.map((p) => {
      if (/^(CommandOrControl|CmdOrCtrl)$/i.test(p)) return platform === "darwin" ? "Cmd" : "Ctrl";
      if (/^(Command|Cmd)$/i.test(p)) return "Cmd";
      if (/^(Control|Ctrl)$/i.test(p)) return "Ctrl";
      if (/^(Alt|Option)$/i.test(p)) return "Alt";
      return p;
    }),
  );
  return [...["Cmd", "Ctrl", "Alt", "Shift"].filter((m) => mods.has(m)), key].join("+");
}

function items(template: MenuItemConstructorOptions[]): MenuItemConstructorOptions[] {
  return template.flatMap((item) => [item, ...(Array.isArray(item.submenu) ? items(item.submenu) : [])]);
}

function claimed(platform: NodeJS.Platform): string[] {
  return items(appMenuTemplate(platform, "Omniplex")).flatMap((item) => [
    ...(item.role ? roleChords(item.role, platform) : []),
    ...(item.accelerator ? [normalise(item.accelerator, platform)] : []),
  ]);
}

function roles(template: MenuItemConstructorOptions[] | undefined): string[] {
  return items(template ?? []).flatMap((item) => (item.role ? [item.role] : []));
}

// The chords the web UI takes in the desktop app (COMMANDS in
// web/src/lib/keybindings.ts), plus reload, which would drop an unsent draft.
function rendererChords(platform: NodeJS.Platform): string[] {
  const mod = platform === "darwin" ? "Cmd" : "Ctrl";
  const keys = ["W", "N", "T", "K", "B", ",", ".", "/", "R", ..."123456789"];
  const shifted = ["[", "]", "N", "M", "E", "C", "D", "R"];
  return [
    ...keys.map((k) => `${mod}+${k}`),
    ...shifted.map((k) => `${mod}+Shift+${k}`),
    `${mod}+Alt+B`,
  ];
}

describe("appMenuTemplate", () => {
  it.each(PLATFORMS)("leaves the renderer's shortcuts free on %s", (platform) => {
    const taken = new Set(claimed(platform));
    expect(rendererChords(platform).filter((c) => taken.has(c))).toEqual([]);
  });

  it.each(PLATFORMS)("keeps the native edit commands on %s", (platform) => {
    const mod = platform === "darwin" ? "Cmd" : "Ctrl";
    expect(claimed(platform)).toEqual(
      expect.arrayContaining(["Z", "X", "C", "V", "A"].map((k) => `${mod}+${k}`)),
    );
  });

  it("puts hide and quit in the macOS app menu", () => {
    const [appMenu] = appMenuTemplate("darwin", "Omniplex");
    expect(appMenu!.label).toBe("Omniplex");
    expect(roles(appMenu!.submenu as MenuItemConstructorOptions[])).toEqual(
      expect.arrayContaining(["hide", "hideOthers", "quit"]),
    );
    expect(claimed("darwin")).toEqual(expect.arrayContaining(["Cmd+Q", "Cmd+H"]));
  });

  it.each(["win32", "linux"] as const)("puts quit in a File menu on %s", (platform) => {
    const template = appMenuTemplate(platform, "Omniplex");
    const file = template.find((m) => m.label === "File");
    expect(roles(file?.submenu as MenuItemConstructorOptions[])).toContain("quit");
    expect(roles(template)).not.toContain("hide");
  });
});
