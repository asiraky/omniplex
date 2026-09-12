/**
 * Where a paste came from, when it came from us.
 *
 * The clipboard carries no provenance: text copied out of the panel's terminal
 * and text copied off a web page arrive at the composer as the same
 * indistinguishable string. But "terminal 1's output" and "App.tsx lines
 * 40–91" are exactly what makes a collapsed chip readable instead of being a
 * grey lump labelled "pasted text" — so a copy made *inside* this app records
 * where it happened, and a paste looks itself up here.
 *
 * Only a fingerprint is kept, never the text. A copied file can be megabytes
 * and the clipboard already holds it; holding a second reference until the
 * next copy evicts it is a leak with no upside. Length plus both ends is
 * enough to tell one recent copy from another, and a false match is harmless:
 * the worst case is a correct chip with the wrong subtitle.
 */

import type { BlobOrigin } from "~/lib/composerRefs";

/** How many recent copies to remember. Small on purpose: provenance more than
    a few copies old is stale, and someone who copied five things since is not
    pasting the first one. */
const KEEP = 4;

/** How much of each end to fingerprint. */
const EDGE = 128;

interface Record {
  len: number;
  head: string;
  tail: string;
  origin: BlobOrigin;
}

let recent: Record[] = [];

function fingerprint(text: string): Omit<Record, "origin"> {
  return { len: text.length, head: text.slice(0, EDGE), tail: text.slice(-EDGE) };
}

/** Remember that this text was copied from here. */
export function recordCopy(text: string, origin: BlobOrigin): void {
  if (!text) return;
  const record = { ...fingerprint(text), origin };
  // Re-copying the same thing moves it to the front rather than filling the
  // list with duplicates of one selection someone hit ⌘C on twice.
  recent = [record, ...recent.filter((r) => !same(r, record))].slice(0, KEEP);
}

/** What this text was copied from, or a plain paste if we never saw it. */
export function originOf(text: string): BlobOrigin {
  const probe = fingerprint(text);
  return recent.find((r) => same(r, probe))?.origin ?? { kind: "paste" };
}

function same(a: Omit<Record, "origin">, b: Omit<Record, "origin">): boolean {
  return a.len === b.len && a.head === b.head && a.tail === b.tail;
}

/** Test seam; nothing in the app clears this. */
export function resetCopyOrigins(): void {
  recent = [];
}

/**
 * Attach a copy recorder to a subtree.
 *
 * Native `copy` bubbles, so one listener on the surface's root covers whatever
 * is selected inside it — an xterm's own selection layer included — without
 * every row having to know about this module. `origin` is a callback rather
 * than a value so a file surface can work out which lines were selected at the
 * moment of the copy.
 */
export function watchCopies(
  host: HTMLElement,
  origin: (text: string) => BlobOrigin | null,
): () => void {
  const onCopy = () => {
    // Read the selection rather than the event's clipboardData: xterm writes
    // the clipboard itself, and on the legacy path there is no data to read.
    const text = window.getSelection()?.toString() ?? "";
    if (!text) return;
    const where = origin(text);
    if (where) recordCopy(text, where);
  };
  host.addEventListener("copy", onCopy);
  return () => host.removeEventListener("copy", onCopy);
}
