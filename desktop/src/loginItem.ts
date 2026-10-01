// Starting at login. macOS registers the app itself and can tell us afterwards
// that it was launched that way; Windows registers a Run entry, which carries
// --hidden so the launch can be recognised. Linux has no API here.
export const HIDDEN_ARG = "--hidden";

export function loginItemSupported(platform: NodeJS.Platform): boolean {
  return platform === "darwin" || platform === "win32";
}

// A launch at login starts in the tray: nobody asked for a window.
export function launchedHidden(opts: {
  platform: NodeJS.Platform;
  argv: readonly string[];
  wasOpenedAtLogin: boolean;
}): boolean {
  if (opts.argv.includes(HIDDEN_ARG)) return true;
  return opts.platform === "darwin" && opts.wasOpenedAtLogin;
}

export function loginItemSettings(openAtLogin: boolean, execPath: string) {
  return { openAtLogin, path: execPath, args: [HIDDEN_ARG] };
}

// Windows looks the entry up by path and arguments, so ask with the same ones.
export function loginItemQuery(execPath: string) {
  return { path: execPath, args: [HIDDEN_ARG] };
}
