import { app, BrowserWindow, shell } from "electron";
import { classifyNavigation, type DesktopAction } from "./routing";
import type { LogFile } from "./log";

type Content = { kind: "page"; url: string } | { kind: "server"; origin: string; path: string };

// The one app window. It can be closed at any time without affecting the
// server; showing it again recreates it with whatever it should be showing.
export class MainWindow {
  private win: BrowserWindow | null = null;
  private content: Content | null = null;

  constructor(
    private readonly opts: {
      icon?: string;
      log: LogFile;
      onAction: (action: DesktopAction) => void;
    },
  ) {}

  isOpen(): boolean {
    return this.win !== null;
  }

  show(): void {
    if (!this.win) this.create();
    const win = this.win!;
    if (win.isMinimized()) win.restore();
    win.show();
    win.focus();
    if (process.platform === "darwin") void app.dock?.show();
  }

  showPage(url: string): void {
    if (this.content?.kind === "page" && this.content.url === url) return;
    this.content = { kind: "page", url };
    this.load();
  }

  showServer(origin: string, path: string): void {
    this.content = { kind: "server", origin, path };
    this.load();
  }

  // The server origin the window is on, if it is.
  serverOrigin(): string | null {
    return this.content?.kind === "server" ? this.content.origin : null;
  }

  private load(): void {
    if (!this.win || !this.content) return;
    const url = this.content.kind === "page" ? this.content.url : this.content.origin + this.content.path;
    // The path is a one-off (the setup screen, say): a window opened later
    // starts on the app.
    if (this.content.kind === "server") this.content = { ...this.content, path: "/" };
    this.win.loadURL(url).catch((err: { code?: string; message?: string }) => {
      // Superseded by a newer load; not a failure.
      if (err?.code === "ERR_ABORTED") return;
      const where = url.startsWith("data:") ? "built-in page" : url;
      this.opts.log.line(`window failed to load ${where}: ${err?.code ?? String(err?.message ?? err)}`);
    });
  }

  private create(): void {
    const win = new BrowserWindow({
      width: 1280,
      height: 860,
      minWidth: 360,
      minHeight: 480,
      title: "Omniplex",
      show: false,
      icon: this.opts.icon,
      autoHideMenuBar: true,
      backgroundColor: "#eef2ff",
      webPreferences: {
        contextIsolation: true,
        nodeIntegration: false,
        sandbox: true,
      },
    });
    this.win = win;

    win.on("closed", () => {
      this.win = null;
      // The app lives on in the menu bar; the Dock icon comes back with a window.
      if (process.platform === "darwin") app.dock?.hide();
    });

    const contents = win.webContents;
    contents.setWindowOpenHandler(({ url }) => {
      this.route(url);
      return { action: "deny" };
    });
    contents.on("will-navigate", (event, url) => {
      if (classifyNavigation(url, this.serverOrigin()).kind === "allow") return;
      event.preventDefault();
      this.route(url);
    });
    contents.on("did-navigate", (_e, url) => {
      if (!url.startsWith("data:")) this.opts.log.line(`window loaded ${url}`);
    });

    this.load();
  }

  private route(url: string): void {
    const decision = classifyNavigation(url, this.serverOrigin());
    if (decision.kind === "external") void shell.openExternal(decision.url);
    else if (decision.kind === "action") this.opts.onAction(decision.action);
    else if (decision.kind === "allow") {
      // A same-origin window.open (an artefact, say) goes to the browser too:
      // the app is one window, and loopback needs no pairing.
      void shell.openExternal(url);
    }
  }
}
