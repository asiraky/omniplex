import type { MenuItemConstructorOptions } from "electron";

// The application menu. It replaces Electron's default, which binds
// Cmd/Ctrl+W to close the window and Cmd/Ctrl+R to reload, both of which lose
// an unsent draft. The web UI handles its own shortcuts (Cmd/Ctrl+W included),
// so the menu keeps only what needs the native side: the edit commands that
// make copy and paste work on macOS, hide and quit, zoom, and devtools.
// Items are listed one by one: the composite roles (windowMenu and friends)
// bring close and reload back.
export function appMenuTemplate(platform: NodeJS.Platform, appName: string): MenuItemConstructorOptions[] {
  const mac = platform === "darwin";
  const menus: MenuItemConstructorOptions[] = [];

  if (mac) {
    menus.push({
      label: appName,
      submenu: [
        { role: "about" },
        { type: "separator" },
        { role: "hide" },
        { role: "hideOthers" },
        { role: "unhide" },
        { type: "separator" },
        { role: "quit" },
      ],
    });
  } else {
    menus.push({ label: "File", submenu: [{ role: "quit" }] });
  }

  menus.push(
    {
      label: "Edit",
      submenu: [
        { role: "undo" },
        { role: "redo" },
        { type: "separator" },
        { role: "cut" },
        { role: "copy" },
        { role: "paste" },
        ...(mac ? [{ role: "pasteAndMatchStyle" } as const] : []),
        { role: "delete" },
        { type: "separator" },
        { role: "selectAll" },
      ],
    },
    {
      label: "View",
      submenu: [
        { role: "resetZoom" },
        { role: "zoomIn" },
        { role: "zoomOut" },
        { type: "separator" },
        { role: "togglefullscreen" },
        { role: "toggleDevTools" },
      ],
    },
    {
      label: "Window",
      // On macOS this makes it the system Window menu (window list and
      // tiling), which adds no close item.
      ...(mac ? { role: "window" as const } : {}),
      submenu: [{ role: "minimize" }, ...(mac ? [{ role: "zoom" } as const] : [])],
    },
  );

  return menus;
}
