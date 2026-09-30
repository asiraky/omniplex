import { app, shell } from "electron";
import os from "node:os";
import path from "node:path";
import { LogFile } from "./log";
import { launchedHidden, loginItemQuery, loginItemSettings, loginItemSupported } from "./loginItem";
import { errorPage, loadingPage } from "./pages";
import { anyFreePort, candidatePorts, choosePort, DEFAULT_PORT, isPortFree } from "./ports";
import { initialRoute, parseSetup, type DesktopAction } from "./routing";
import { serverBinaryPath, ServerSupervisor, type ServerStatus } from "./server";
import { resolveServerPath, withPath } from "./shellPath";
import { StateFile } from "./state";
import { AppTray } from "./tray";
import { Updater } from "./updater";
import { MainWindow } from "./window";

// A second launch (a double-click on the app while it sits in the tray) hands
// over to the running one, which shows its window.
if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  run();
}

function run(): void {
  const platform = process.platform;
  const hidden = launchedHidden({
    platform,
    argv: process.argv,
    wasOpenedAtLogin: platform === "darwin" && app.getLoginItemSettings().wasOpenedAtLogin,
  });
  const iconDir = app.isPackaged
    ? path.join(process.resourcesPath, "icons")
    : path.join(app.getAppPath(), "resources", "icons");

  let quitting = false;
  let mainWindow: MainWindow | undefined;
  let server: ServerSupervisor;
  let tray: AppTray | undefined;
  let updater: Updater | undefined;

  app.on("second-instance", () => mainWindow?.show());
  // Closing the last window is not quitting: the server keeps serving phones.
  app.on("window-all-closed", () => undefined);
  app.on("activate", () => mainWindow?.show());

  app.on("before-quit", (event) => {
    if (quitting || !server) return;
    event.preventDefault();
    quitting = true;
    void server.stop().finally(() => {
      tray?.destroy();
      app.quit();
    });
  });
  for (const signal of ["SIGINT", "SIGTERM"] as const) process.on(signal, () => app.quit());

  if (platform === "darwin" && hidden) app.dock?.hide();

  void app.whenReady().then(async () => {
    const log = new LogFile(app.getPath("logs"), !app.isPackaged);
    const state = new StateFile(path.join(app.getPath("userData"), "desktop-state.json"));
    log.line(`Omniplex desktop ${app.getVersion()} starting${hidden ? " hidden" : ""}`);

    // Login items are only registered for an installed app; a dev build
    // would otherwise register the Electron binary from node_modules.
    const canLogin = app.isPackaged && loginItemSupported(platform);
    if (canLogin && !state.get().loginItemConfigured) {
      app.setLoginItemSettings(loginItemSettings(true, process.execPath));
      state.update({ loginItemConfigured: true });
    }

    const win = new MainWindow({
      icon: platform === "linux" ? path.join(iconDir, "window.png") : undefined,
      log,
      onAction: (action: DesktopAction) => {
        if (action === "retry") server.retry();
        else shell.showItemInFolder(log.path);
      },
    });
    mainWindow = win;
    win.showPage(loadingPage());
    if (!hidden) win.show();

    if (app.isPackaged) updater = new Updater(log, (s) => tray?.setUpdate(s));

    let routedOrigin: string | null = null;
    const onStatus = async (status: ServerStatus) => {
      tray?.setServer(status);
      if (status.kind === "failed") {
        routedOrigin = null;
        win.showPage(errorPage({ message: status.message, logPath: log.path }));
        // Nobody will see an error in a window that is not open.
        win.show();
      } else if (status.kind === "starting" && routedOrigin === null) {
        win.showPage(loadingPage());
      } else if (status.kind === "running") {
        state.update({ lastPort: status.port });
        // A restart after a crash on the same port: the UI reconnects by
        // itself, so leave it where it is.
        if (routedOrigin === status.origin && win.serverOrigin() === status.origin) return;
        routedOrigin = status.origin;
        const route = initialRoute({
          firstRun: !state.get().setupShown,
          setup: await fetchSetup(status.origin, log),
        });
        if (route === "/setup") state.update({ setupShown: true });
        log.line(`opening ${route}`);
        win.showServer(status.origin, route);
      }
    };

    const preferred = Number(process.env.OMNIPLEX_PORT) || DEFAULT_PORT;
    const envPath = await resolveServerPath({ platform, env: process.env, home: os.homedir() });
    const binary = serverBinaryPath({
      isPackaged: app.isPackaged,
      resourcesPath: process.resourcesPath,
      appPath: app.getAppPath(),
      platform,
      env: process.env,
    });

    server = new ServerSupervisor({
      binary,
      env: { ...withPath(process.env, envPath), OMNIPLEX_DESKTOP: "1" },
      log,
      pickPort: () =>
        choosePort(candidatePorts({ preferred, lastUsed: state.get().lastPort }), (p) => isPortFree(p), () => anyFreePort()),
      onStatus: (s) => void onStatus(s),
    });

    tray = new AppTray({
      iconDir,
      loginItemSupported: canLogin,
      updatesSupported: updater !== undefined,
      actions: {
        open: () => win.show(),
        quit: () => app.quit(),
        checkForUpdates: () => updater?.checkNow(),
        installUpdate: () => {
          quitting = true;
          void server.stop().then(() => updater?.install());
        },
        openAtLogin: () => app.getLoginItemSettings(loginItemQuery(process.execPath)).openAtLogin,
        setOpenAtLogin: (enabled) => app.setLoginItemSettings(loginItemSettings(enabled, process.execPath)),
      },
    });
    tray.setServer(server.current());

    server.start();
    updater?.start();
  });
}

async function fetchSetup(origin: string, log: LogFile) {
  try {
    const res = await fetch(`${origin}/api/setup`, { signal: AbortSignal.timeout(10000) });
    if (!res.ok) {
      log.line(`GET /api/setup: HTTP ${res.status}`);
      return null;
    }
    return parseSetup(await res.json());
  } catch (err) {
    log.line(`GET /api/setup failed: ${String(err)}`);
    return null;
  }
}
