/**
 * Artefacts: files a thread shows the user, from the agent or uploaded, a
 * page, a report, a picture, a spreadsheet. They stay where they are on the
 * server and the panel reads them live.
 *
 * This module is the client's whole model of them: the shapes the server
 * sends, the URLs that read them back, and the rules that decide how each type
 * is badged and which viewer shows it. Nothing here renders, so the transcript
 * can badge an artefact without pulling the viewers into the first bundle.
 */

/**
 * A file or folder the thread has shown: a file on the server, read live.
 * The agent revises it in place and shows it again, which updates this record
 * rather than adding another.
 */
export interface Artefact {
  id: string;
  name: string;
  /** Absolute, on the server. */
  path: string;
  /** A folder rather than a single file. */
  dir?: boolean;
  mediaType: string;
  /** Bytes, total across a folder's files. */
  size: number;
  /** The file to show, relative to a folder, or the file's own name. */
  entry: string;
  files: number;
  /** Newest file's modification time when last shown, in millis. Keys the
      viewer's cache, so a revision is refetched and nothing else is. */
  modifiedAt: number;
  source: "agent" | "upload";
  /** The agent's one line on what it is or what changed. */
  note?: string;
  turnId?: string;
  shownAt: number;
}

/** How a prompt names an uploaded file. */
export interface ArtefactRef {
  artefactId: string;
}

/** Matches the server's upload cap. Checked here so a phone finds out before
    it spends minutes of 4G on a file that will be refused. */
export const MAX_ARTEFACT_BYTES = 200 * 1024 * 1024;

// ---- URLs and requests ----

const base = (threadId: string, artefactId: string) =>
  `/api/threads/${encodeURIComponent(threadId)}/artefacts/${encodeURIComponent(artefactId)}`;

/** Raw bytes of one file, as it is on disk now. Each path segment is encoded
    on its own so a folder's subdirectories survive as directories. `rev` is
    the artefact's modifiedAt: the server ignores it, but it gives each
    revision its own URL, so nothing keyed on the URL shows a stale one. */
export function rawUrl(
  threadId: string,
  artefactId: string,
  path: string,
  opts: { download?: boolean; rev?: number } = {},
): string {
  const encoded = path.split("/").map(encodeURIComponent).join("/");
  const q = new URLSearchParams();
  if (opts.rev !== undefined) q.set("m", String(opts.rev));
  if (opts.download) q.set("download", "1");
  const query = q.toString();
  return `${base(threadId, artefactId)}/f/${encoded}${query ? `?${query}` : ""}`;
}

export interface ExpiringUrl {
  url: string;
  expiresAt: number;
}

async function call<T>(method: string, url: string): Promise<T> {
  const res = await fetch(url, { method });
  if (!res.ok) throw new Error((await errorText(res)) || `request failed (${res.status})`);
  return (await res.json()) as T;
}

async function errorText(res: Response): Promise<string> {
  return res
    .json()
    .then((b: { error?: string }) => b.error ?? "")
    .catch(() => "");
}

/** A sandboxed, tokenised URL for an HTML artefact: `/p/<token>/index.html`.
    It serves the live files, so a revision shows on reload. */
export function requestPreview(threadId: string, artefactId: string): Promise<ExpiringUrl> {
  return call<ExpiringUrl>("POST", `${base(threadId, artefactId)}/preview`);
}

/** A share link: a copy of the artefact taken when it was shared. */
export interface ShareLink {
  url: string;
  sharedAt: number;
  expiresAt: number;
}

type ShareReply = { share: ShareLink | null };

/** The artefact's share link, or null when it has none. */
export function getShare(threadId: string, artefactId: string): Promise<ShareLink | null> {
  return call<ShareReply>("GET", `${base(threadId, artefactId)}/share`).then((r) => r.share);
}

/** Shares the artefact as it is now. When it is already shared, the link stays
    the same and starts showing the files as they are now. */
export function share(threadId: string, artefactId: string): Promise<ShareLink> {
  return call<ShareReply>("POST", `${base(threadId, artefactId)}/share`).then((r) => r.share!);
}

/** Stops sharing: the link stops working, and sharing again makes a new one. */
export function unshare(threadId: string, artefactId: string): Promise<void> {
  return call<ShareReply>("DELETE", `${base(threadId, artefactId)}/share`).then(() => undefined);
}

export interface UploadedArtefact {
  artefact: Artefact;
}

/**
 * Uploads one file as a new artefact. XHR rather than fetch because fetch
 * reports nothing about an upload until it is over, and a 40 MB PDF on a train
 * is minutes of nothing without a progress bar.
 */
export function uploadArtefact(
  threadId: string,
  file: File,
  opts: { signal?: AbortSignal; onProgress?: (fraction: number) => void } = {},
): Promise<UploadedArtefact> {
  return new Promise((resolve, reject) => {
    if (opts.signal?.aborted) {
      reject(new DOMException("Aborted", "AbortError"));
      return;
    }
    const xhr = new XMLHttpRequest();
    const name = file.name || "file";
    xhr.open("POST", `/api/threads/${encodeURIComponent(threadId)}/artefacts?name=${encodeURIComponent(name)}`);
    xhr.setRequestHeader("Content-Type", file.type || "application/octet-stream");
    xhr.responseType = "text";
    const onAbort = () => xhr.abort();
    opts.signal?.addEventListener("abort", onAbort, { once: true });
    const done = () => opts.signal?.removeEventListener("abort", onAbort);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && e.total > 0) opts.onProgress?.(e.loaded / e.total);
    };
    xhr.onload = () => {
      done();
      let body: unknown;
      try {
        body = JSON.parse(xhr.responseText);
      } catch {
        body = undefined;
      }
      if (xhr.status >= 200 && xhr.status < 300 && body) {
        resolve(body as UploadedArtefact);
        return;
      }
      const message = (body as { error?: string } | undefined)?.error;
      reject(new Error(message || `upload failed (${xhr.status})`));
    };
    xhr.onerror = () => {
      done();
      reject(new Error("upload failed: the connection dropped"));
    };
    xhr.onabort = () => {
      done();
      reject(new DOMException("Aborted", "AbortError"));
    };
    xhr.send(file);
  });
}

// ---- types: badge, family, viewer ----

export type TypeFamily = "html" | "doc" | "image" | "media" | "data" | "code" | "other";

export type ViewerKind =
  | "html"
  | "markdown"
  | "image"
  | "svg"
  | "audio"
  | "video"
  | "pdf"
  | "csv"
  | "json"
  | "text"
  | "fallback";

/** Lower-cased extension without the dot, or "" when there is none. */
export function extensionOf(name: string): string {
  const base = name.slice(name.lastIndexOf("/") + 1).toLowerCase();
  const dot = base.lastIndexOf(".");
  return dot <= 0 ? "" : base.slice(dot + 1);
}

/** The media type without parameters: "text/plain; charset=utf-8" → "text/plain". */
function essence(mediaType: string): string {
  return mediaType.split(";")[0]!.trim().toLowerCase();
}

const BROWSER_IMAGES = new Set(["png", "jpg", "jpeg", "gif", "webp", "avif", "bmp", "ico"]);
const AUDIO = new Set(["mp3", "wav", "ogg", "oga", "m4a", "aac", "flac", "opus", "weba"]);
const VIDEO = new Set(["mp4", "webm", "mov", "m4v", "ogv"]);
const MARKDOWN = new Set(["md", "markdown", "mdx"]);
const DOCS = new Set(["pdf", "doc", "docx", "odt", "rtf", "txt", "pages", "ppt", "pptx", "odp", "key", "epub", "tex"]);
const DATA = new Set([
  "csv", "tsv", "json", "jsonl", "ndjson", "xls", "xlsx", "ods", "numbers", "parquet", "sqlite", "db", "arrow", "avro",
]);
// Things a person reads as code, which the text viewer shows with line numbers.
const CODE = new Set([
  "js", "mjs", "cjs", "jsx", "ts", "mts", "cts", "tsx", "go", "rs", "py", "rb", "java", "kt", "swift", "c", "h",
  "cpp", "cc", "hpp", "cs", "php", "lua", "r", "scala", "dart", "ex", "exs", "erl", "hs", "clj", "ml", "zig",
  "sh", "bash", "zsh", "fish", "ps1", "bat", "css", "scss", "sass", "less", "sql", "graphql", "gql", "proto",
  "yaml", "yml", "toml", "ini", "cfg", "conf", "env", "xml", "xsl", "vue", "svelte", "astro", "tf", "hcl",
  "dockerfile", "makefile", "mk", "cmake", "gradle", "diff", "patch", "vim", "nix",
]);
// Text that is neither code nor a document with its own viewer.
const PLAIN_TEXT = new Set(["txt", "log", "text", "rst", "adoc", "org", "srt", "vtt", "gitignore", "lock"]);

const TEXTY_APPLICATION = [
  "application/json",
  "application/xml",
  "application/javascript",
  "application/ecmascript",
  "application/typescript",
  "application/x-typescript",
  "application/yaml",
  "application/x-yaml",
  "application/toml",
  "application/x-sh",
  "application/x-shellscript",
  "application/sql",
  "application/graphql",
  "application/x-httpd-php",
  "application/x-python",
  "application/x-ndjson",
  "application/ld+json",
];

function isTextMedia(mt: string): boolean {
  return mt.startsWith("text/") || mt.endsWith("+json") || mt.endsWith("+xml") || TEXTY_APPLICATION.includes(mt);
}

/**
 * Which viewer shows a file. The extension is asked first because agents and
 * browsers are both sloppy with media types — a `.ts` upload arrives as
 * `video/mp2t`, a `.md` as `application/octet-stream` — while the name is
 * whatever the author called it. The media type decides only what the name
 * cannot.
 */
export function viewerFor(entry: string, mediaType: string): ViewerKind {
  const ext = extensionOf(entry);
  const mt = essence(mediaType);
  if (ext === "html" || ext === "htm" || ext === "xhtml") return "html";
  if (MARKDOWN.has(ext)) return "markdown";
  if (ext === "svg") return "svg";
  if (ext === "pdf") return "pdf";
  if (ext === "csv" || ext === "tsv") return "csv";
  if (ext === "json" || ext === "geojson" || ext === "jsonc" || ext === "webmanifest") return "json";
  if (BROWSER_IMAGES.has(ext)) return "image";
  if (AUDIO.has(ext)) return "audio";
  if (VIDEO.has(ext)) return "video";
  if (CODE.has(ext) || PLAIN_TEXT.has(ext)) return "text";

  if (mt === "text/html" || mt === "application/xhtml+xml") return "html";
  if (mt === "text/markdown" || mt === "text/x-markdown") return "markdown";
  if (mt === "image/svg+xml") return "svg";
  if (mt === "application/pdf") return "pdf";
  if (mt === "text/csv" || mt === "text/tab-separated-values") return "csv";
  if (mt === "application/json" || mt.endsWith("+json")) return "json";
  if (mt.startsWith("image/") && ["png", "jpeg", "gif", "webp", "avif", "bmp"].some((t) => mt === `image/${t}`)) return "image";
  if (mt.startsWith("audio/")) return "audio";
  if (mt.startsWith("video/")) return "video";
  if (isTextMedia(mt)) return "text";
  return "fallback";
}

/** Whether a viewer has a rendered form and a source form to flip between. */
export function hasSourceView(kind: ViewerKind): boolean {
  return kind === "html" || kind === "markdown" || kind === "svg" || kind === "csv" || kind === "json";
}

/** Which viewers read the file as text rather than pointing an element at it. */
export function readsText(kind: ViewerKind, mode: ViewMode): boolean {
  if (kind === "markdown" || kind === "csv" || kind === "json" || kind === "text") return true;
  return (kind === "html" || kind === "svg") && mode === "source";
}

/** The badge colour group — a hint at what kind of thing it is, not a legend. */
export function typeFamily(name: string, mediaType: string): TypeFamily {
  const ext = extensionOf(name);
  const mt = essence(mediaType);
  if (ext === "html" || ext === "htm" || mt === "text/html") return "html";
  if (ext === "svg" || BROWSER_IMAGES.has(ext) || ["heic", "heif", "tif", "tiff", "psd"].includes(ext) || mt.startsWith("image/"))
    return "image";
  if (AUDIO.has(ext) || VIDEO.has(ext) || mt.startsWith("audio/") || mt.startsWith("video/")) return "media";
  if (MARKDOWN.has(ext) || DOCS.has(ext) || mt === "application/pdf" || mt === "text/markdown" || mt === "text/plain")
    return "doc";
  if (DATA.has(ext) || mt === "text/csv" || mt === "application/json" || mt.includes("spreadsheet")) return "data";
  if (CODE.has(ext) || isTextMedia(mt)) return "code";
  return "other";
}

/** The short label on a badge: the extension, or the media subtype when the
    name has none. */
export function badgeLabel(name: string, mediaType: string): string {
  const ext = extensionOf(name);
  if (ext) return ext.slice(0, 5).toUpperCase();
  const sub = essence(mediaType).split("/")[1] ?? "";
  const tidy = sub.replace(/^x-/, "").split(/[.+-]/)[0] ?? "";
  if (!tidy || tidy === "octet") return "FILE";
  return tidy.slice(0, 5).toUpperCase();
}

/** Delimiter for a delimited-text file. */
export function delimiterFor(entry: string, mediaType: string): string {
  return extensionOf(entry) === "tsv" || essence(mediaType) === "text/tab-separated-values" ? "\t" : ",";
}

// ---- preview / source preference ----

export type ViewMode = "preview" | "source";

const viewKey = (kind: ViewerKind) => `omniplex.artefactView:${kind}`;

/** The last preview/source choice for this kind of file. Remembered per kind:
    someone reading CSVs as tables is not asking to see HTML as source. */
export function loadViewMode(kind: ViewerKind): ViewMode {
  try {
    return localStorage.getItem(viewKey(kind)) === "source" ? "source" : "preview";
  } catch {
    return "preview";
  }
}

export function saveViewMode(kind: ViewerKind, mode: ViewMode): void {
  try {
    localStorage.setItem(viewKey(kind), mode);
  } catch {
    // Storage denied (Safari with cookies blocked): the choice lasts the view.
  }
}

// ---- formatting ----

/** "812 B", "4.2 KB", "1.2 MB" — decimal units, one decimal below 10. */
export function formatBytes(n: number): string {
  const v = Math.max(0, n);
  if (v < 1000) return `${Math.round(v)} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let x = v / 1000;
  let i = 0;
  while (x >= 1000 && i < units.length - 1) {
    x /= 1000;
    i++;
  }
  return `${x < 10 ? Math.round(x * 10) / 10 : Math.round(x)} ${units[i]}`;
}

// ---- the attached-files trailer ----

export interface AttachedFile {
  name: string;
  mediaType: string;
  artefactId: string;
}

const OPEN_TAG = "<attached-files>";
const CLOSE_TAG = "</attached-files>";
// `- <name> (<mediaType>, <size>, artefact <id>): <path>`. The name
// is greedy so one with its own parentheses — "report (final).pdf" — keeps
// them: the match settles on the last group shaped like the metadata.
const LINE = /^- (.+) \(([^,()]+), ([^,()]+), artefact ([^\s(),]+)\): .*$/;

/**
 * Splits the server's attached-files trailer off a prompt's text.
 *
 * The server appends the block so the agent sees where each upload lives on
 * disk; the person who sent it wants their words and a row of tiles, not a list
 * of host paths. Only a block that closes the message is a trailer — the same
 * tag written mid-message is someone's text and is left alone.
 */
export function parseAttachedFiles(text: string): { text: string; files: AttachedFile[] } {
  const trimmed = text.trimEnd();
  if (!trimmed.endsWith(CLOSE_TAG)) return { text, files: [] };
  const open = trimmed.lastIndexOf(OPEN_TAG);
  if (open === -1) return { text, files: [] };
  // The opening tag has to start its own line, or it is inside someone's prose.
  if (open > 0 && trimmed[open - 1] !== "\n") return { text, files: [] };
  const body = trimmed.slice(open + OPEN_TAG.length, trimmed.length - CLOSE_TAG.length);
  const files: AttachedFile[] = [];
  for (const raw of body.split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    const m = LINE.exec(line);
    if (!m) continue;
    files.push({ name: m[1]!, mediaType: m[2]!.trim(), artefactId: m[4]! });
  }
  if (files.length === 0) return { text, files: [] };
  return { text: trimmed.slice(0, open).trimEnd(), files };
}

// ---- the preview token ----

/** Swaps the token in a preview URL for a fresh one, keeping the page and its
    query and hash — so a reload after expiry lands where the reader was. */
export function retoken(current: string, fresh: string): string {
  const token = /\/p\/[^/]+\//;
  const next = token.exec(fresh);
  if (!next || !token.test(current)) return fresh;
  return current.replace(token, next[0]);
}

/** A preview URL as the reader thinks of it: the path inside the folder, with
    the origin and token stripped. Anything outside the bundle is shown whole. */
export function bundlePath(url: string): string {
  try {
    const u = new URL(url, window.location.origin);
    const m = /^\/p\/[^/]+(\/.*)$/.exec(u.pathname);
    if (!m || u.origin !== window.location.origin) return url;
    return decodeURIComponent(m[1]!) + u.search + u.hash;
  } catch {
    return url;
  }
}
