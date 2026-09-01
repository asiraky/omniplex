// Where `git clone <url>` would put the checkout, worked out in the browser so
// the destination field can be filled in as the URL is typed rather than after
// a round trip. Git's own rule is simple enough to copy: strip the trailing
// `.git` and any trailing slashes, then take the last path segment.

/**
 * The folder name git would clone into. "" when nothing recognisable has been
 * typed yet, so the caller can leave the destination alone rather than
 * prefilling a path ending in nothing.
 */
export function repoName(source: string): string {
  let text = source.trim();
  if (!text) return "";
  // A URL query or fragment is not part of the path.
  text = text.split(/[?#]/, 1)[0];
  // scp-style (`git@github.com:owner/repo.git`) has no scheme and uses a colon
  // where a URL would use a slash. Everything after the first colon is the
  // path; the host in front of it never names the repo.
  const scp = /^[^/]*@[^/:]+:(.*)$/.exec(text);
  if (scp) text = scp[1];
  // ssh:// and https:// both leave the path after the authority, and a bare
  // `owner/repo` is already a path.
  else if (text.includes("://")) {
    const rest = text.slice(text.indexOf("://") + 3);
    const slash = rest.indexOf("/");
    text = slash === -1 ? "" : rest.slice(slash + 1);
  }

  text = text.replace(/\/+$/, "");
  if (!text) return "";
  const last = text.slice(text.lastIndexOf("/") + 1);
  // `.git` is a suffix on the name, not a name: `repo.git` and `repo` clone to
  // the same folder. A repo genuinely called `.git` is not a thing.
  const name = last.replace(/\.git$/i, "");
  // Path segments that are not names at all.
  if (name === "" || name === "." || name === "..") return "";
  return name;
}

/** Trailing slashes off, so joining never doubles them. */
function trimEnd(path: string): string {
  return path.replace(/\/+$/, "");
}

/**
 * The destination to prefill: the parent directory the operator keeps
 * checkouts in, plus the name git would have chosen. "" when the URL does not
 * name a repo yet — an empty field says "still typing" better than a path that
 * is only a directory.
 */
export function cloneDestination(parent: string, source: string): string {
  const name = repoName(source);
  if (!name) return "";
  const base = trimEnd(parent.trim());
  return base ? `${base}/${name}` : name;
}

/**
 * The directory a clone landed in, to prefill the next one from. "" when the
 * path has no parent to speak of, which is not worth remembering.
 */
export function parentDirectory(path: string): string {
  const text = trimEnd(path.trim());
  const slash = text.lastIndexOf("/");
  if (slash < 0) return "";
  // A checkout directly under the root: the parent is "/" itself.
  if (slash === 0) return "/";
  return text.slice(0, slash);
}
