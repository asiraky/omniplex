// Deciding whether a span of inline code is a file path. Only inline code is
// considered — agents backtick paths nearly always, and walking arbitrary
// prose would misfire constantly. The defenses here are borrowed from t3code:
// a TLD denylist so `example.com` is not a path, an extensionless allowlist so
// `Makefile` is, and a rule that a candidate must have a path prefix, an
// extension, or a `:line` suffix to count at all.

export interface DetectedPath {
  /** The path as written, minus any leading ./ and any :line:col anchor. */
  path: string;
  line?: number;
}

// Hosts' most common TLDs. A dot-separated candidate whose last segment is one
// of these, with no slash and no line anchor, is a domain being mentioned, not
// a file.
const TLDS = new Set([
  "com",
  "org",
  "net",
  "io",
  "dev",
  "app",
  "ai",
  "co",
  "edu",
  "gov",
  "info",
  "biz",
  "xyz",
  "me",
  "tv",
  "sh",
  "gg",
  "au",
  "uk",
  "de",
  "fr",
  "jp",
  "nz",
]);

// Extensionless names that are files anyway.
const BARE_FILES = new Set([
  "makefile",
  "dockerfile",
  "rakefile",
  "gemfile",
  "justfile",
  "procfile",
  "readme",
  "license",
  "codeowners",
  "changelog",
  "contributing",
  "authors",
]);

// Everything a path may be made of. Spaces, backticks and most punctuation
// exclude a candidate outright.
const SHAPE = /^[A-Za-z0-9_\-./@+~]+$/;

/**
 * Decide whether one inline-code span is a file path, and split off any
 * `:line` / `:line:col` anchor. Returns null for anything that does not look
 * strongly enough like a path to be worth a chip.
 */
export function detectPath(raw: string): DetectedPath | null {
  let text = raw.trim();
  if (text.length < 2 || text.length > 260) return null;
  if (text.includes("://") || text.startsWith("www.")) return null;

  // Split a trailing :12 or :12:5 anchor before judging the rest.
  let line: number | undefined;
  const anchor = /:(\d+)(?::\d+)?$/.exec(text);
  if (anchor) {
    line = Number(anchor[1]);
    text = text.slice(0, anchor.index);
  }

  if (!SHAPE.test(text)) return null;
  // An explicit relative (`./`) or absolute (`/`) prefix is itself a strong
  // path signal, independent of any extension.
  const explicitRel = text.startsWith("./");
  const absolute = text.startsWith("/");
  if (explicitRel) text = text.slice(2);
  if (text === "" || text === "." || text === "..") return null;
  // A parent-relative path points outside anything we can open.
  if (text.startsWith("../") || text.includes("/../")) return null;
  // Flag-looking spans (`--no-color`) and lone dotfiles of punctuation.
  if (text.startsWith("-")) return null;

  // A single trailing slash (`web/`, `web/src/`) is an explicit directory
  // reference — a strong path signal even without an extension.
  const trailingSlash = text.endsWith("/");
  if (trailingSlash) text = text.slice(0, -1);
  if (text === "") return null;
  const hasSlash = text.includes("/");
  const base = hasSlash ? text.slice(text.lastIndexOf("/") + 1) : text;
  if (base === "") return null;
  const dot = base.lastIndexOf(".");
  const ext = dot > 0 ? base.slice(dot + 1).toLowerCase() : "";

  // A slash command or skill (`/code-review`, `/loop`, `/init`): one leading
  // slash, one segment, no extension and no line anchor. Real absolute paths
  // worth opening have a directory above the file (`/etc/hosts`,
  // `/Users/...`), so requiring a second segment costs nothing and stops every
  // `/skill` mention from rendering as a file chip. A dotfile basename
  // (`/.env`) is exempt — no slash command starts with a dot. A bare filename
  // is deliberately NOT exempt here: `/readme` is far likelier to be a skill
  // than a file at the filesystem root.
  const dotfile = base.startsWith(".") && base.length > 1;
  const bareName =
    BARE_FILES.has(base.toLowerCase()) ||
    BARE_FILES.has(base.toLowerCase().replace(/\.(md|txt|rst)$/, ""));
  if (
    absolute &&
    !text.slice(1).includes("/") &&
    ext === "" &&
    line === undefined &&
    !trailingSlash &&
    !dotfile
  ) {
    return null;
  }

  // A domain: dotted, slashless, anchorless, ending in a TLD.
  if (!hasSlash && line === undefined && ext !== "" && TLDS.has(ext))
    return null;

  // The admission rule: a path prefix, or an extension, or a line anchor, or a
  // known bare filename. A bare word (`filter`, `main`) is none of these.
  if (hasSlash) {
    // All-numeric segments are a date or a fraction (`1/2/2024`), not a path.
    const segments = text.split("/").filter(Boolean);
    if (segments.every((s) => /^\d+$/.test(s))) return null;
    // A slash alone is too weak: `system/init`, `turn/interrupt` and `a/b`
    // alternatives (`rawMaxTokens/maxTokens`) are protocol/method names, not
    // files. Admit only with a corroborating path signal: an extension on the
    // basename (`foo/bar.ts`), a known bare filename (`docs/README`), a `:line`
    // anchor, an explicit directory (trailing slash, `web/src/`), or an
    // explicit relative/absolute prefix (`./scripts/build`, `/usr/bin/dev`).
    // The extension must sit on the basename, so a dotted hostname segment
    // (`github.com/asiraky/omniplex`) does not masquerade as one.
    const hasExt =
      ext !== "" && /^[a-z0-9]{1,10}$/.test(ext) && !/^\d+$/.test(ext);
    if (
      !hasExt &&
      !bareName &&
      line === undefined &&
      !trailingSlash &&
      !explicitRel &&
      !absolute
    ) {
      return null;
    }
    return { path: text, line };
  }
  if (bareName) {
    return { path: text, line };
  }
  if (ext !== "" && /^[a-z0-9]{1,10}$/.test(ext) && !/^\d+$/.test(ext)) {
    return { path: text, line };
  }
  if (line !== undefined && dot > 0) {
    return { path: text, line };
  }
  return null;
}

/**
 * A path a shell in the checkout printed, relative to the checkout, or null
 * when it points somewhere the server will not serve: the home directory,
 * above the checkout, or an absolute path outside it. The checkout is `cwd`;
 * a relative path is taken as relative to it, which is where a thread's
 * terminal starts. The checkout itself comes back as "".
 */
export function pathInCheckout(path: string, cwd: string): string | null {
  if (path.startsWith("~")) return null;
  let rel = path;
  if (path.startsWith("/")) {
    const root = cwd.replace(/\/+$/, "") + "/";
    if (!cwd || !(path + "/").startsWith(root)) return null;
    rel = path.slice(root.length);
  }
  // A trailing slash is how `git status` marks a directory; the panel's
  // routing matches directories by bare path, so it goes.
  rel = rel.replace(/^(?:\.\/)+/, "").replace(/^\.$/, "").replace(/\/+$/, "");
  if (rel === ".." || rel.startsWith("../") || rel.includes("/../")) return null;
  return rel;
}
