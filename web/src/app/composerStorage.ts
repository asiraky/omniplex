// What the composers hold, kept in localStorage so a reload gets it back. A
// phone discards a backgrounded tab without asking, and coming back to it is a
// fresh page load: without this every half-written prompt and every prompt
// still waiting on its ack is gone.
//
// One key per thread, and per outbox entry, rather than one blob: two tabs
// then write different keys and cannot clobber each other.
//
// Every access is wrapped. Storage can be denied outright (Safari private
// mode) or full; that costs the memory, never the interaction.

import { uuid } from "~/client";
import { attachmentUrl, type Attachment } from "~/lib/attachments";
import type { Delivery } from "~/protocol";

import type { DraftStore } from "./draftStore";
import type { FailedSend, Outbox, OutboxEntry } from "./outbox";

const DRAFT_PREFIX = "omniplex.draft.v1:";
const ATTACHMENTS_PREFIX = "omniplex.attachments.v1:";
const OUTBOX_PREFIX = "omniplex.outbox.v1:";
const TAB_KEY = "omniplex.tab.v1";

/** Long enough to skip the writes between keystrokes, short enough that a
    crash loses a word rather than a paragraph. */
export const DRAFT_DEBOUNCE_MS = 400;

/** How long another tab's pending send is taken to be that tab's, still
    waiting on its ack. A live tab on a dead connection fails it within the
    client's command deadline and probe, under 20s, but a hidden one runs its
    timers up to a minute late. */
const ABANDONED_AFTER_MS = 60_000;

/** How old a send still unanswered at load can be and still go by itself.
    Older than this, the person has moved on: it comes back to the composer
    with a note instead of turning up in the thread out of nowhere. */
const LAPSED_AFTER_MS = 10 * 60_000;

/** Why a lapsed send is shown as not sent. */
export const LAPSED_REASON = "the page closed before the server answered";

function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function write(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Denied or full.
  }
}

function remove(key: string) {
  try {
    localStorage.removeItem(key);
  } catch {
    // Denied.
  }
}

/** Every stored key under the prefix, paired with what follows it. */
function keysUnder(prefix: string): [key: string, id: string][] {
  const found: [string, string][] = [];
  try {
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);
      if (key && key.startsWith(prefix) && key.length > prefix.length) {
        found.push([key, key.slice(prefix.length)]);
      }
    }
  } catch {
    // Denied: nothing was kept.
  }
  return found;
}

function parse(raw: string | null): unknown {
  if (raw === null) return undefined;
  try {
    return JSON.parse(raw);
  } catch {
    return undefined;
  }
}

const isRecord = (v: unknown): v is Record<string, unknown> =>
  typeof v === "object" && v !== null && !Array.isArray(v);

const optional = (v: unknown, type: "string" | "number") => v === undefined || typeof v === type;

// Drafts

export function loadDrafts(): Record<string, string> {
  const drafts: Record<string, string> = {};
  for (const [key, threadId] of keysUnder(DRAFT_PREFIX)) {
    const text = read(key);
    if (text) drafts[threadId] = text;
  }
  return drafts;
}

/**
 * Writes the store's drafts back. Debounced, since the store changes per
 * keystroke, and flushed at once when the page is hidden or torn down: a
 * discard happens to a hidden tab, where a pending timer would never fire.
 *
 * Only threads whose text changed since the last write are touched, so a
 * thread another tab is typing into is left alone until this tab changes it.
 * The baseline is what storage holds, not what the store was seeded with: the
 * caller may have changed a draft on load, and that has to reach storage now.
 *
 * A draft emptied, which is what sending does, is written at once: its prompt
 * is in the outbox already, and a page that died with the old text still
 * stored would come back holding a prompt that is also being sent again.
 */
export function persistDrafts(store: DraftStore): () => void {
  let written: Readonly<Record<string, string>> = loadDrafts();
  let timer: ReturnType<typeof setTimeout> | undefined;

  const flush = () => {
    clearTimeout(timer);
    timer = undefined;
    const next = store.all();
    if (next === written) return;
    for (const [threadId, text] of Object.entries(next)) {
      if ((written[threadId] ?? "") === text) continue;
      if (text) write(DRAFT_PREFIX + threadId, text);
      else remove(DRAFT_PREFIX + threadId);
    }
    for (const threadId of Object.keys(written)) {
      if (!(threadId in next)) remove(DRAFT_PREFIX + threadId);
    }
    written = next;
  };

  const schedule = () => {
    const next = store.all();
    for (const [threadId, text] of Object.entries(written)) {
      if (text && !next[threadId]) return flush();
    }
    clearTimeout(timer);
    timer = setTimeout(flush, DRAFT_DEBOUNCE_MS);
  };
  const onVisibility = () => {
    if (document.visibilityState === "hidden") flush();
  };

  flush();
  const unsubscribe = store.subscribe(schedule);
  document.addEventListener("visibilitychange", onVisibility);
  window.addEventListener("pagehide", flush);

  return () => {
    unsubscribe();
    document.removeEventListener("visibilitychange", onVisibility);
    window.removeEventListener("pagehide", flush);
    flush();
  };
}

// Attachments

/** An attachment as stored: the server's reference to it, nothing tied to
    this page. */
type StoredRef = Omit<Attachment, "previewUrl" | "progress" | "error">;

/**
 * What of a composer's attachments survives a reload. Only ready ones are
 * kept: a staged File cannot be stored, and an upload cut off by the reload
 * would have to be picked again anyway.
 */
function storedRefs(list: readonly Attachment[] | undefined): StoredRef[] {
  const refs: StoredRef[] = [];
  for (const a of list ?? []) {
    if (a.status !== "ready" || !(a.id || a.artefactId)) continue;
    const { previewUrl, progress, error, ...ref } = a;
    refs.push(ref);
  }
  return refs;
}

/** A stored ref back as an attachment, its thumbnail pointed at the server's
    copy since the object URL died with the page. */
function revive(threadId: string, v: unknown): Attachment | undefined {
  if (!isRecord(v)) return undefined;
  const { key, name, kind, status, id, artefactId, mediaType, size } = v;
  if (typeof key !== "string" || typeof name !== "string" || status !== "ready") return undefined;
  if (kind !== undefined && kind !== "image" && kind !== "file") return undefined;
  if (!optional(id, "string") || !optional(artefactId, "string")) return undefined;
  if (!optional(mediaType, "string") || !optional(size, "number")) return undefined;
  if (!id && !artefactId) return undefined;
  const a: Attachment = {
    key,
    name,
    status,
    previewUrl: kind !== "file" && typeof id === "string" ? attachmentUrl(threadId, id) : "",
  };
  if (kind !== undefined) a.kind = kind;
  if (id !== undefined) a.id = id as string;
  if (artefactId !== undefined) a.artefactId = artefactId as string;
  if (mediaType !== undefined) a.mediaType = mediaType as string;
  if (size !== undefined) a.size = size as number;
  return a;
}

function reviveAll(threadId: string, v: unknown): Attachment[] {
  if (!Array.isArray(v)) return [];
  return v.map((a) => revive(threadId, a)).filter((a): a is Attachment => !!a);
}

export function loadAttachments(): Record<string, Attachment[]> {
  const all: Record<string, Attachment[]> = {};
  for (const [key, threadId] of keysUnder(ATTACHMENTS_PREFIX)) {
    const list = reviveAll(threadId, parse(read(key)));
    if (list.length) all[threadId] = list;
  }
  return all;
}

/** Writes the threads whose list changed. Upload progress replaces a list many
    times without changing what would be stored, so those skip the write. */
export function saveAttachments(
  prev: Record<string, Attachment[]>,
  next: Record<string, Attachment[]>,
) {
  for (const [threadId, list] of Object.entries(next)) {
    const before = prev[threadId];
    if (before === list) continue;
    const refs = storedRefs(list);
    const json = JSON.stringify(refs);
    if (before && JSON.stringify(storedRefs(before)) === json) continue;
    if (refs.length) write(ATTACHMENTS_PREFIX + threadId, json);
    else remove(ATTACHMENTS_PREFIX + threadId);
  }
  for (const threadId of Object.keys(prev)) {
    if (!(threadId in next)) remove(ATTACHMENTS_PREFIX + threadId);
  }
}

// Outbox

/** Which tab wrote an entry. Kept in sessionStorage, which a tab keeps across
    its own reloads and shares with no other tab. */
function thisTab(): string {
  try {
    let id = sessionStorage.getItem(TAB_KEY);
    if (!id) {
      id = uuid();
      sessionStorage.setItem(TAB_KEY, id);
    }
    return id;
  } catch {
    return fallbackTab;
  }
}
const fallbackTab = uuid();

function isDelivery(v: unknown): v is Delivery {
  return v === "steer" || v === "queue";
}

function reviveEntry(v: unknown): { entry: OutboxEntry; tab: unknown } | undefined {
  if (!isRecord(v)) return undefined;
  const { commandId, threadId, text, status, reason, dismissed, sentAt, attachments, delivery, tab } =
    v;
  if (typeof commandId !== "string" || typeof threadId !== "string" || typeof text !== "string")
    return undefined;
  if (typeof sentAt !== "number") return undefined;
  const send = {
    commandId,
    threadId,
    text,
    sentAt,
    attachments: reviveAll(threadId, attachments),
    ...(isDelivery(delivery) ? { delivery } : {}),
  };
  if (status === "pending") return { entry: { ...send, status }, tab };
  if (status !== "failed" || typeof reason !== "string") return undefined;
  const entry: FailedSend = { ...send, status, reason };
  if (dismissed === true) entry.dismissed = true;
  return { entry, tab };
}

/**
 * Entries left by earlier pages, oldest send first, and which of them lapsed.
 *
 * A pending one this tab sent never heard its ack before the page died: it
 * comes back pending, for the caller to send again under its id, and the
 * server's ledger replays the result if it did arrive. So does one from
 * another tab that has been pending too long for that tab to be alive. One
 * another tab sent moments ago is that tab's, still waiting on its ack, and
 * is left alone.
 *
 * A pending one sent longer ago than anyone would still expect it to arrive
 * lapses instead: it comes back failed, and listed as lapsed, for the caller
 * to put its text back in the composer.
 *
 * A failed one already had its text restored, which the persisted draft kept,
 * so it only comes back as it was: from any tab, so a retry from this one is
 * still a retry.
 */
export function loadOutbox(): { entries: OutboxEntry[]; lapsed: FailedSend[] } {
  const tab = thisTab();
  const now = Date.now();
  const entries: OutboxEntry[] = [];
  const lapsed: FailedSend[] = [];
  for (const [key] of keysUnder(OUTBOX_PREFIX)) {
    const found = reviveEntry(parse(read(key)));
    if (!found) continue;
    const { entry } = found;
    const age = now - entry.sentAt;
    if (entry.status === "pending" && found.tab !== tab && age < ABANDONED_AFTER_MS) continue;
    if (entry.status === "pending" && age >= LAPSED_AFTER_MS) {
      const failed: FailedSend = { ...entry, status: "failed", reason: LAPSED_REASON };
      entries.push(failed);
      lapsed.push(failed);
    } else entries.push(entry);
  }
  const bySent = (a: OutboxEntry, b: OutboxEntry) => a.sentAt - b.sentAt;
  return { entries: entries.sort(bySent), lapsed: lapsed.sort(bySent) };
}

/**
 * Mirrors the outbox to storage on every change. It changes once per send and
 * once per ack, so each change is written at once, one key per entry: a tab
 * writes and removes only the entries it holds, and an ack in one tab cannot
 * erase a send another tab is still waiting on.
 *
 * The first write claims what this tab loaded, so a pending send it took over
 * from a dead tab is this tab's from then on.
 */
export function persistOutbox(outbox: Outbox): () => void {
  let written: readonly OutboxEntry[] = [];
  const sync = () => {
    const next = outbox.get();
    const before = new Set(written);
    const kept = new Set(next.map((e) => e.commandId));
    const tab = thisTab();
    for (const e of next) {
      if (before.has(e)) continue;
      const stored = { ...e, attachments: storedRefs(e.attachments), tab };
      write(OUTBOX_PREFIX + e.commandId, JSON.stringify(stored));
    }
    for (const e of written) if (!kept.has(e.commandId)) remove(OUTBOX_PREFIX + e.commandId);
    written = next;
  };
  sync();
  return outbox.subscribe(sync);
}
