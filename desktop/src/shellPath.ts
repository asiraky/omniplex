import { execFile } from "node:child_process";
import path from "node:path";

// An app started from Finder, the Dock or at login inherits launchd's PATH
// (/usr/bin:/bin:/usr/sbin:/sbin), so git from Homebrew and claude or codex
// from npm are invisible to the server. The user's login shell knows the real
// PATH; ask it, and add the usual install locations in case it is slow,
// broken, or configured somewhere a non-interactive run does not read.

const MARKER = "__OMNIPLEX_PATH__";

// Printed between markers so banners, motd and prompt noise from rc files do
// not end up in PATH. Quoted "$PATH" is colon-joined in fish as well.
export const LOGIN_SHELL_SCRIPT = `printf '%s%s%s' '${MARKER}' "$PATH" '${MARKER}'`;

export function parseLoginShellPath(stdout: string): string | undefined {
  const start = stdout.indexOf(MARKER);
  if (start < 0) return undefined;
  const end = stdout.indexOf(MARKER, start + MARKER.length);
  if (end < 0) return undefined;
  const value = stdout.slice(start + MARKER.length, end).trim();
  return value || undefined;
}

// Joins PATH lists in priority order, dropping empties and repeats.
export function mergePaths(delimiter: string, ...lists: Array<string | undefined>): string {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const list of lists) {
    for (const raw of (list ?? "").split(delimiter)) {
      const dir = raw.trim();
      const key = delimiter === ";" ? dir.toLowerCase() : dir;
      if (!dir || seen.has(key)) continue;
      seen.add(key);
      out.push(dir);
    }
  }
  return out.join(delimiter);
}

// Where git, claude and codex land when installed the documented ways.
export function knownToolDirs(platform: NodeJS.Platform, home: string, env: NodeJS.ProcessEnv): string[] {
  if (platform === "win32") {
    const win = path.win32;
    const dirs = [win.join(home, ".local", "bin")];
    if (env.APPDATA) dirs.push(win.join(env.APPDATA, "npm"));
    if (env.LOCALAPPDATA) dirs.push(win.join(env.LOCALAPPDATA, "Programs", "Git", "cmd"));
    dirs.push(win.join(env.ProgramFiles ?? "C:\\Program Files", "Git", "cmd"));
    return dirs;
  }
  const dirs = [
    path.posix.join(home, ".local", "bin"),
    path.posix.join(home, ".claude", "local"),
    path.posix.join(home, ".bun", "bin"),
    path.posix.join(home, ".npm-global", "bin"),
    path.posix.join(home, ".volta", "bin"),
  ];
  if (platform === "darwin") dirs.push("/opt/homebrew/bin", "/opt/homebrew/sbin");
  dirs.push("/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin");
  return dirs;
}

export function readLoginShellPath(shell: string, timeoutMs = 5000): Promise<string | undefined> {
  return new Promise((resolve) => {
    execFile(
      shell,
      ["-ilc", LOGIN_SHELL_SCRIPT],
      { timeout: timeoutMs, killSignal: "SIGKILL", maxBuffer: 1 << 20, env: { ...process.env, TERM: "dumb" } },
      (_err, stdout) => resolve(parseLoginShellPath(String(stdout ?? ""))),
    );
  });
}

// PATH for the server: the login shell's first, then what this process was
// given, then the fallbacks.
export async function resolveServerPath(opts: {
  platform: NodeJS.Platform;
  env: NodeJS.ProcessEnv;
  home: string;
  readShellPath?: (shell: string) => Promise<string | undefined>;
}): Promise<string> {
  const { platform, env, home } = opts;
  const current = getPath(env);
  if (platform === "win32") {
    return mergePaths(";", current, knownToolDirs(platform, home, env).join(";"));
  }
  const shell = env.SHELL || (platform === "darwin" ? "/bin/zsh" : "/bin/sh");
  const fromShell = await (opts.readShellPath ?? readLoginShellPath)(shell);
  return mergePaths(":", fromShell, current, knownToolDirs(platform, home, env).join(":"));
}

// Windows environment keys are case-insensitive but a copied env object is
// not: writing PATH beside an existing Path leaves the child two of them.
export function pathKey(env: NodeJS.ProcessEnv): string {
  return Object.keys(env).find((k) => k.toUpperCase() === "PATH") ?? "PATH";
}

export function getPath(env: NodeJS.ProcessEnv): string | undefined {
  return env[pathKey(env)];
}

export function withPath(env: NodeJS.ProcessEnv, value: string): NodeJS.ProcessEnv {
  return { ...env, [pathKey(env)]: value };
}
