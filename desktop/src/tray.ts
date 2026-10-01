import { Menu, Tray, nativeImage } from "electron";
import path from "node:path";
import type { ServerStatus } from "./server";
import type { UpdateState } from "./updater";

export interface TrayActions {
  open: () => void;
  quit: () => void;
  checkForUpdates: () => void;
  installUpdate: () => void;
  setOpenAtLogin: (enabled: boolean) => void;
  openAtLogin: () => boolean;
}

export interface TrayOptions {
  iconDir: string;
  loginItemSupported: boolean;
  updatesSupported: boolean;
  actions: TrayActions;
}

// The menu bar (macOS) or notification area (Windows) item that keeps the app,
// and the server, alive with no window open.
export class AppTray {
  private readonly tray: Tray;
  private server: ServerStatus = { kind: "starting" };
  private update: UpdateState = { kind: "idle" };

  constructor(private readonly opts: TrayOptions) {
    const image =
      process.platform === "darwin"
        ? nativeImage.createFromPath(path.join(opts.iconDir, "trayTemplate.png"))
        : nativeImage.createFromPath(path.join(opts.iconDir, "tray.png"));
    if (process.platform === "darwin") image.setTemplateImage(true);
    this.tray = new Tray(image);
    this.tray.setToolTip("Omniplex");
    // macOS opens the menu on click; elsewhere a click means "show me the app".
    if (process.platform !== "darwin") this.tray.on("click", () => opts.actions.open());
    this.render();
  }

  setServer(status: ServerStatus): void {
    this.server = status;
    this.render();
  }

  setUpdate(state: UpdateState): void {
    this.update = state;
    this.render();
  }

  destroy(): void {
    this.tray.destroy();
  }

  private render(): void {
    const { actions } = this.opts;
    const items: Electron.MenuItemConstructorOptions[] = [
      { label: "Open Omniplex", click: actions.open },
      { label: serverLabel(this.server), enabled: false },
      { type: "separator" },
    ];
    if (this.opts.loginItemSupported) {
      items.push({
        label: "Open at login",
        type: "checkbox",
        checked: actions.openAtLogin(),
        click: (item) => actions.setOpenAtLogin(item.checked),
      });
    }
    if (this.update.kind === "ready") {
      items.push({ label: `Restart to update to ${this.update.version}`, click: actions.installUpdate });
    } else {
      items.push({
        label: this.update.kind === "checking" ? "Checking for updates…" : "Check for updates",
        enabled: this.opts.updatesSupported && this.update.kind !== "checking",
        click: actions.checkForUpdates,
      });
    }
    items.push({ type: "separator" }, { label: "Quit Omniplex", click: actions.quit });
    this.tray.setContextMenu(Menu.buildFromTemplate(items));
  }
}

function serverLabel(status: ServerStatus): string {
  switch (status.kind) {
    case "starting":
      return "Server starting…";
    case "running":
      return `Server running on port ${status.port}`;
    case "failed":
      return "Server stopped: open Omniplex for details";
    case "stopped":
      return "Server stopped";
  }
}
