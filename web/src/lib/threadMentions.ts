/**
 * `@thread` in the composer: a pointer to another thread in the same project,
 * staged as a chip beside the attachments and sent as one markdown link per
 * line at the end of the prompt, `[@Title](omniplex-thread://<id>)`. The agent
 * reads the thread itself (`thread_read`); nothing of it is inlined, so the
 * mention costs the wire one line.
 *
 * The transcript turns those links back into chips.
 */

import { createContext } from "react";

import { searchThreads } from "~/components/palette/search";
import type { ComposerItem, ThreadMeta } from "~/protocol";

export const THREAD_SCHEME = "omniplex-thread://";

/** A thread staged in the composer to go with the next message. */
export interface ThreadMention {
  id: string;
  title: string;
}

/** How many threads the `@` menu offers at once. */
export const MENTION_LIMIT = 20;

// Backslash-escaped, so a title with brackets in it cannot end the link text
// early. Brackets and parentheses are all CommonMark needs; a backslash is
// escaped too, or one ending a title would escape the closing bracket.
const escapeTitle = (title: string) => title.replace(/[\\[\]()]/g, (c) => `\\${c}`);
const unescapeTitle = (title: string) => title.replace(/\\(.)/g, "$1");

export function mentionLink(m: ThreadMention): string {
  return `[@${escapeTitle(m.title || "Untitled")}](${THREAD_SCHEME}${m.id})`;
}

/** The prompt as sent: the text, then each mention as a link on its own line. */
export function withMentions(text: string, mentions: readonly ThreadMention[]): string {
  if (mentions.length === 0) return text;
  const links = mentions.map(mentionLink).join("\n");
  return text ? `${text}\n\n${links}` : links;
}

export type MessagePart = { kind: "text"; text: string } | ({ kind: "thread" } & ThreadMention);

const LINK = /\[@((?:\\.|[^\]\\])*)\]\(omniplex-thread:\/\/([^)\s]+)\)/g;

/**
 * A message's thread links, read back out of it. `mentions` is the block the
 * composer appended: the links at the very end, one per line, with the line
 * breaks that set them apart. `parts` is everything before it, text and any
 * link that sits among the words, in order. A message with no links is one
 * text part and no mentions.
 */
export function splitMentions(text: string): { parts: MessagePart[]; mentions: ThreadMention[] } {
  const all: MessagePart[] = [];
  let at = 0;
  for (const m of text.matchAll(LINK)) {
    const start = m.index ?? 0;
    if (start > at) all.push({ kind: "text", text: text.slice(at, start) });
    all.push({ kind: "thread", id: m[2], title: unescapeTitle(m[1]) });
    at = start + m[0].length;
  }
  if (at < text.length) all.push({ kind: "text", text: text.slice(at) });
  let tail = all.length;
  while (tail > 0) {
    const p = all[tail - 1];
    if (p.kind === "text" && p.text.trim()) break;
    tail--;
  }
  const mentions = all
    .slice(tail)
    .flatMap((p) => (p.kind === "thread" ? [{ id: p.id, title: p.title }] : []));
  if (mentions.length === 0) return { parts: all, mentions };
  const parts = all.slice(0, tail);
  const last = parts[parts.length - 1];
  if (last?.kind === "text") parts[parts.length - 1] = { kind: "text", text: last.text.replace(/\s+$/, "") };
  return { parts, mentions };
}

/** The threads an `@` in this thread's composer can name: the others in its
    project. An assistant chat belongs to no project, so it names none. */
export function mentionCandidates(threads: ThreadMeta[], current: ThreadMeta | undefined): ThreadMeta[] {
  const project = current?.projectId;
  if (!project) return [];
  return threads.filter((t) => t.projectId === project && t.id !== current.id);
}

const ITEM_PREFIX = "thread:";

/** Stands in for the `@` trigger while there are threads to name, so the
    menu's trigger detection sees it. It is never offered or sent. */
export const MENTION_TRIGGER: ComposerItem = {
  id: "client:thread-mention",
  name: "",
  kind: "command",
  trigger: "@",
  insertText: "@",
  behavior: "client-action",
  inline: true,
};

/** The `@` menu's entries for a query, best title match first. */
export function mentionItems(candidates: ThreadMeta[], query: string): ComposerItem[] {
  return searchThreads(candidates, query, MENTION_LIMIT).map((t) => ({
    id: `${ITEM_PREFIX}${t.id}`,
    name: t.title || "Untitled",
    description: [t.harness, t.model].filter(Boolean).join(" · "),
    kind: "command",
    trigger: "@",
    // What the menu shows the entry as. Choosing it types nothing: the token
    // goes, and the thread becomes a chip.
    insertText: `@${t.title || "Untitled"}`,
    behavior: "client-action",
    action: `${ITEM_PREFIX}${t.id}`,
    inline: true,
  }));
}

/**
 * Where a chosen `@query` token is cut from the draft: the token, and one
 * space after it when one before it already keeps the words apart, so taking
 * it out of "look at @plan please" leaves one space, not two.
 */
export function mentionCut(text: string, start: number, end: number): { start: number; end: number } {
  const before = start === 0 || /\s/.test(text[start - 1] ?? "");
  return before && text[end] === " " ? { start, end: end + 1 } : { start, end };
}

/** The thread a chosen `@` entry names, or null for any other entry. */
export function mentionedThread(item: ComposerItem): string | null {
  return item.action?.startsWith(ITEM_PREFIX) ? item.action.slice(ITEM_PREFIX.length) : null;
}

/** Opens a thread by id, for chips in the transcript. Null where nothing can. */
export const OpenThreadContext = createContext<((id: string) => void) | null>(null);
