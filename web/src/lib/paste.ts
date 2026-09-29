/**
 * A long paste, turned into a file.
 *
 * Pasting a log, a spec or a page of notes into the composer used to leave a
 * wall of text in a box meant for a sentence, and then the same wall in the
 * transcript. Past a size, the paste becomes a file instead: a tile in the
 * composer, a tile in the transcript, and the agent is told where it is saved.
 * The file's type follows the text, so pasted markdown opens formatted.
 */

/** A paste this long, or this many lines, goes in as a file. Below both, it is
    something typed-sized and stays in the box. */
export const FILE_OVER_CHARS = 2000;
export const FILE_OVER_LINES = 20;

export type PasteKind = "markdown" | "json" | "text";

const EXT: Record<PasteKind, string> = { markdown: "md", json: "json", text: "txt" };
const TYPE: Record<PasteKind, string> = {
  markdown: "text/markdown",
  json: "application/json",
  text: "text/plain",
};

/** The file a paste should become, or null to leave it in the textarea. */
export function pastedFile(text: string): File | null {
  if (!isLong(text)) return null;
  const kind = pasteKind(text);
  const name = `${(kind === "markdown" && headingSlug(text)) || "pasted"}.${EXT[kind]}`;
  return new File([text], name, { type: TYPE[kind] });
}

function isLong(text: string): boolean {
  if (text.length > FILE_OVER_CHARS) return true;
  let lines = 1;
  for (const c of text) if (c === "\n" && ++lines > FILE_OVER_LINES) return true;
  return false;
}

/**
 * What the text looks like. JSON when it parses as an object or array. Markdown
 * on a fence or a table, which nothing else writes, or on two of the looser
 * signs: a `#` heading alone is also a shell or Python comment, and a `- ` line
 * alone is also a plain list.
 */
export function pasteKind(text: string): PasteKind {
  const trimmed = text.trim();
  if (/^[[{]/.test(trimmed)) {
    try {
      JSON.parse(trimmed);
      return "json";
    } catch {
      // Looked like JSON and was not: judge it as text.
    }
  }
  if (/^\s*(```|~~~)/m.test(text)) return "markdown";
  if (/^\s*\|.*\|\s*$/m.test(text) && /^\s*\|?\s*:?-{3,}:?\s*\|/m.test(text)) return "markdown";
  const signs = [
    /^#{1,6}\s+\S/m,
    /^\s*[-*+]\s+\S/m,
    /^\s*\d+[.)]\s+\S/m,
    /\[[^\]\n]+\]\([^)\s]+\)/,
    /(\*\*|__)[^*_\n]+\1/,
    /^>\s/m,
    /`[^`\n]+`/,
  ].filter((re) => re.test(text)).length;
  return signs >= 2 ? "markdown" : "text";
}

/** The first heading as a file name: "## Release notes, v2" → "release-notes-v2". */
function headingSlug(text: string): string {
  const heading = /^#{1,6}\s+(.+)$/m.exec(text)?.[1] ?? "";
  return heading
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 48)
    .replace(/-+$/, "");
}
