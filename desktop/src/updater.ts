import { dialog } from "electron";
import { autoUpdater } from "electron-updater";
import type { LogFile } from "./log";

export type UpdateState = { kind: "idle" } | { kind: "checking" } | { kind: "ready"; version: string };

const CHECK_EVERY_MS = 4 * 60 * 60 * 1000;

// Updates come from GitHub Releases (the publish block in
// electron-builder.config.cjs). They download in the background and install
// when the app quits, or straight away from the tray.
export class Updater {
  private manual = false;
  private timer: NodeJS.Timeout | null = null;

  constructor(
    private readonly log: LogFile,
    private readonly onState: (state: UpdateState) => void,
  ) {
    autoUpdater.logger = {
      info: (m: unknown) => log.line(`updater: ${String(m)}`),
      warn: (m: unknown) => log.line(`updater: ${String(m)}`),
      error: (m: unknown) => log.line(`updater error: ${String(m)}`),
      debug: () => undefined,
    };
    autoUpdater.autoDownload = true;
    autoUpdater.autoInstallOnAppQuit = true;

    autoUpdater.on("checking-for-update", () => this.onState({ kind: "checking" }));
    autoUpdater.on("update-not-available", (info) => {
      this.onState({ kind: "idle" });
      if (this.manual) {
        this.manual = false;
        void dialog.showMessageBox({ message: "Omniplex is up to date.", detail: `Version ${info.version}` });
      }
    });
    autoUpdater.on("update-available", (info) => {
      this.log.line(`update ${info.version} available; downloading`);
      if (this.manual) {
        this.manual = false;
        void dialog.showMessageBox({
          message: `Omniplex ${info.version} is downloading.`,
          detail: "It will install when you restart Omniplex.",
        });
      }
    });
    autoUpdater.on("update-downloaded", (info) => this.onState({ kind: "ready", version: info.version }));
    autoUpdater.on("error", (err) => {
      this.onState({ kind: "idle" });
      if (this.manual) {
        this.manual = false;
        void dialog.showMessageBox({ type: "warning", message: "Couldn't check for updates.", detail: err.message });
      }
    });
  }

  start(): void {
    const check = () => autoUpdater.checkForUpdatesAndNotify().catch(() => undefined);
    void check();
    this.timer = setInterval(check, CHECK_EVERY_MS);
  }

  checkNow(): void {
    this.manual = true;
    autoUpdater.checkForUpdates().catch(() => undefined);
  }

  // The caller stops the server first so the installer can replace its binary.
  install(): void {
    if (this.timer) clearInterval(this.timer);
    autoUpdater.quitAndInstall();
  }
}
