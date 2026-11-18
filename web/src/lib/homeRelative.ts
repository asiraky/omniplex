/** `cwd` as the prompt would write it: under `home`, with `~` for it. */
export function homeRelative(cwd: string, home: string): string {
  if (cwd === home) return "~";
  const base = home.endsWith("/") ? home : `${home}/`;
  return cwd.startsWith(base) ? `~/${cwd.slice(base.length)}` : cwd;
}
