// Finding the URLs and file paths in a line of terminal output, so the
// terminal can make them clickable. Ported from T3 Code's terminal-links.ts,
// cut down to what a POSIX shell prints: no PowerShell prompts, drive letters,
// backslashes or UNC shares.
//
// Hover detection runs this on every pointer move, so every pattern here has
// to stay linear in the length of the line: a long run of output with no
// separators must not be rescanned from every suffix.

export type TerminalLink =
  | { kind: "url"; start: number; end: number; url: string }
  | { kind: "path"; start: number; end: number; path: string; line?: number };

interface Range {
  start: number;
  end: number;
}

const URL_PATTERN = /https?:\/\/[^\s"'`<>]+/giu;

// Two shapes of path. One with an explicit prefix (`/`, `./`, `../`, `~/`)
// runs to the next space or quote and is tidied up afterwards. One without a
// prefix is built from segments and starts only at a segment boundary, which is
// what keeps it linear. In those segments `[` needs its `]` (trimming a long
// run of `]` would otherwise be quadratic) and parentheses count only before a
// slash, so `src/foo.ts(3,10)` stays a compiler location. The bare shape also
// matches slashless words (`main.go:12`, `foo.bar`); admit() sorts them out.
const PATH_PATTERN =
  /(?:~\/|\.{1,2}\/|\/)[^\s"'`<>]+|(?<![\p{L}\p{M}\p{N}._-])[\p{L}\p{M}\p{N}._-]+(?:\/(?:[\p{L}\p{M}\p{N}._$+@-]|\[{1,2}[\p{L}\p{M}\p{N}._-]+\]{1,2}|\([\p{L}\p{M}\p{N}._-]+\)(?=\/))+)*\/?(?::\d+){0,2}/gu;
const PREFIXED = /^(?:~\/|\.{1,2}\/|\/)/;

// Trimmed by hand, not with /[.,;!?]+$/: that regex retries from every
// character of a long run of dots and goes quadratic.
const URL_TRAILING_PUNCTUATION = ".,;!?";
// Paths also drop a trailing colon: compilers end `file:line:col:` with one.
const PATH_TRAILING_PUNCTUATION = ".,;:!?";
const ANCHOR = /:(\d+)(?::\d+)?$/;

// A logged request target with a query string is a route, not a file. A
// question mark is legal in a filename, so a route without one is left alone.
const REQUEST_ROUTE_PATTERN =
  /(?:^|[\s"'`])(?:GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS|CONNECT|TRACE|proxying)\s+["'`]?(?:\/|[A-Za-z0-9_-]+\/)[^\s?]*\?\S*/g;
// The tail of `redis://host/0` or `file:///a.mjs` is not a path. A diff's `+`
// or `-` may lead, and the lookbehind keeps a run of letters from being
// rescanned at every suffix.
const SCHEME_URL_PATTERN = /(?<![A-Za-z\d+.-])[+-]?[A-Za-z][A-Za-z\d+.-]+:\/\/[^\s"'`<>]+/gu;
// Nor is the tail of an scp-style Git remote (`git@github.com:acme/widgets.git`).
// Only a path wholly inside the tail is dropped, so `src/contact@support.vue:12`
// keeps its link.
const SCP_REMOTE_PATTERN = /(?<![\w.@-])[\w.-]+@[\w.-]+:[^\s"'`<>]+/gu;

// `api.example.com:443` is a host and port, not a file and line.
const DOMAIN_ENDINGS = new Set(["com", "org", "net"]);

/**
 * Links in one logical terminal line (soft-wrapped rows already joined). start/end are string
 * indices into `line`, end exclusive, sorted by start, non-overlapping.
 */
export function findTerminalLinks(line: string): TerminalLink[] {
  const urls: TerminalLink[] = [];
  for (const m of line.matchAll(URL_PATTERN)) {
    const url = trimEnd(m[0], URL_TRAILING_PUNCTUATION);
    if (/^https?:\/\/./i.test(url)) urls.push({ kind: "url", start: m.index, end: m.index + url.length, url });
  }

  const notPaths: Range[] = [...line.matchAll(REQUEST_ROUTE_PATTERN), ...line.matchAll(SCHEME_URL_PATTERN)].map(
    (m) => ({ start: m.index, end: m.index + m[0].length }),
  );
  const remoteTails: Range[] = Array.from(line.matchAll(SCP_REMOTE_PATTERN), (m) => ({
    start: m.index + m[0].indexOf(":") + 1,
    end: m.index + m[0].length,
  }));

  const paths: TerminalLink[] = [];
  for (const m of line.matchAll(PATH_PATTERN)) {
    const prefixed = PREFIXED.test(m[0]);
    // The prefixed shape runs on into prose, and the bare one takes in the full
    // stop of `Edited src/foo.ts.`, since a dot is part of a segment.
    const text = trimEnd(m[0], PATH_TRAILING_PUNCTUATION);
    const link = admit(text, prefixed);
    if (!link) continue;
    const range = { start: m.index, end: m.index + text.length };
    if (urls.some((u) => overlaps(u, range)) || notPaths.some((r) => overlaps(r, range))) continue;
    if (remoteTails.some((t) => range.start >= t.start && range.end <= t.end)) continue;
    paths.push({ kind: "path", ...range, ...link });
  }

  return [...urls, ...paths].sort((a, b) => a.start - b.start);
}

/** Whether a candidate is path enough to link, and if so its path and line. */
function admit(text: string, prefixed: boolean): { path: string; line?: number } | null {
  const anchor = ANCHOR.exec(text);
  const path = anchor ? text.slice(0, anchor.index) : text;
  const line = anchor ? Number(anchor[1]) : undefined;
  const found = line === undefined ? { path } : { path, line };

  // A prefix is signal enough, so long as something follows it: `//` is a
  // comment, not a file.
  if (prefixed) return /[\p{L}\p{N}]/u.test(path.replace(PREFIXED, "")) ? found : null;

  const segments = path.split("/").filter(Boolean);
  if (segments.length === 0) return null;
  // `3/10` is a counter and `1/2/2024` a date.
  if (segments.every((s) => /^\d+$/.test(s))) return null;
  const base = segments[segments.length - 1];
  const dot = base.lastIndexOf(".");
  const ext = dot > 0 ? base.slice(dot + 1) : "";
  const hasExt = /^[\p{L}\p{N}]{1,10}$/u.test(ext) && !/^\d+$/.test(ext);

  if (path.includes("/")) {
    // A slash alone is too weak: `origin/main`, `KB/s` and `and/or` are not
    // files. It takes an extension, a line, or a trailing slash (`web/`, as
    // `git status` prints an untracked directory) to corroborate it.
    return hasExt || line !== undefined || path.endsWith("/") ? found : null;
  }
  // A slashless word lights up only as `name.ext:line`; `foo.bar` in prose
  // and `localhost:3000` stay plain.
  if (!hasExt || line === undefined || DOMAIN_ENDINGS.has(ext.toLowerCase())) return null;
  return found;
}

/**
 * Drop trailing punctuation and any closing bracket its opener does not
 * account for: `(see /a/b.ts).` ends at `.ts`, `/a/(b)` keeps its paren. Two
 * passes, so punctuation inside the bracket (`(/a/b.ts.)`) goes too; brackets
 * are counted once per pass so a long run of `)` costs a linear scan.
 */
function trimEnd(value: string, punctuation: string): string {
  let out = value;
  for (let pass = 0; pass < 2; pass++) {
    let cut = out.length;
    while (cut > 0 && punctuation.includes(out[cut - 1])) cut--;
    out = out.slice(0, cut);
    for (const [open, close] of [
      ["(", ")"],
      ["[", "]"],
      ["{", "}"],
    ]) {
      let excess = count(out, close) - count(out, open);
      let end = out.length;
      while (excess > 0 && out[end - 1] === close) {
        end--;
        excess--;
      }
      out = out.slice(0, end);
    }
  }
  return out;
}

function count(s: string, ch: string): number {
  let n = 0;
  for (const c of s) if (c === ch) n++;
  return n;
}

function overlaps(a: Range, b: Range): boolean {
  return a.start < b.end && b.start < a.end;
}
