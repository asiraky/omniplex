import type { ComposerItem } from "~/protocol";

export interface ComposerTrigger {
  trigger: string;
  query: string;
  start: number;
  end: number;
  /** The token is the first thing on its line, where every entry applies.
      Anywhere else only the entries a harness acts on mid-prompt do. */
  leading: boolean;
}

/**
 * The trigger the cursor sits in: the whitespace-delimited token under it,
 * when that token starts with a trigger character. `src/foo` and a URL start
 * with something else, so they are not one. Nor is `/usr/bin`: no entry has a
 * slash in its name, so a second one makes the token a path.
 */
export function detectComposerTrigger(
  text: string,
  cursor: number,
  items: ComposerItem[],
): ComposerTrigger | null {
  const safeCursor = Math.max(0, Math.min(text.length, cursor));
  let tokenStart = safeCursor;
  while (tokenStart > 0 && !/\s/.test(text[tokenStart - 1] ?? "")) tokenStart--;
  let tokenEnd = safeCursor;
  while (tokenEnd < text.length && !/\s/.test(text[tokenEnd] ?? "")) tokenEnd++;
  const token = text.slice(tokenStart, safeCursor);
  const lineStart = text.lastIndexOf("\n", Math.max(0, tokenStart - 1)) + 1;
  const leading = text.slice(lineStart, tokenStart).trim() === "";

  // Mid-prompt, a trigger none of whose entries work there is ordinary text:
  // a harness that ignores a command in the middle of a prompt is not offered
  // one.
  const offered = leading ? items : items.filter((item) => item.inline);
  const triggers = [...new Set(offered.map((item) => item.trigger).filter(Boolean))].sort(
    (a, b) => b.length - a.length,
  );
  for (const trigger of triggers) {
    if (!token.startsWith(trigger)) continue;
    const query = token.slice(trigger.length);
    if (trigger === "/" && query.includes("/")) continue;
    return { trigger, query, start: tokenStart, end: tokenEnd, leading };
  }
  return null;
}

export function rankComposerItems(items: ComposerItem[], trigger: ComposerTrigger): ComposerItem[] {
  return items
    .filter((item) => item.trigger === trigger.trigger && (trigger.leading || item.inline))
    .map((item, index) => ({ item, index, score: scoreItem(item, trigger) }))
    .filter((row) => row.score > 0)
    .sort((a, b) => b.score - a.score || a.index - b.index)
    .map((row) => row.item);
}

function scoreItem(item: ComposerItem, trigger: ComposerTrigger): number {
  const query = trigger.query.trim().toLowerCase();
  if (!query) return 1;
  const names = [item.name, ...(item.aliases ?? [])].map((value) => value.toLowerCase());
  let best = 0;
  // Mid-prompt the token is as likely to be prose as a command — `/tmp`, a
  // price — and Enter takes whatever the menu has. So there only a name that
  // contains what was typed counts: no descriptions, no scattered letters.
  if (!trigger.leading) {
    for (const name of names) if (name.includes(query)) best = Math.max(best, matchScore(name, query));
    return best;
  }
  for (const name of names) best = Math.max(best, matchScore(name, query) * 4);
  best = Math.max(best, matchScore(item.description?.toLowerCase() ?? "", query) * 1.5);
  best = Math.max(best, matchScore(item.argsHint?.toLowerCase() ?? "", query));
  return best;
}

function matchScore(text: string, query: string): number {
  if (!text) return 0;
  if (text === query) return 4;
  if (text.startsWith(query)) return 3;
  if (text.includes(query)) return 2;
  if (query.length < 3) return 0;
  let at = 0;
  for (const char of text) {
    if (char === query[at]) at++;
    if (at === query.length) return 1;
  }
  return 0;
}

export function replaceComposerTrigger(text: string, trigger: ComposerTrigger, replacement: string) {
  const value = `${text.slice(0, trigger.start)}${replacement}${text.slice(trigger.end)}`;
  return { value, cursor: trigger.start + replacement.length };
}

/** Match a submitted standalone action and return its unparsed argument tail. */
export function submittedComposerAction(text: string, items: ComposerItem[]) {
  const trimmed = text.trim();
  for (const item of items) {
    if (item.behavior === "prompt") continue;
    if (trimmed === item.insertText) return { item, args: "" };
    if (
      trimmed.startsWith(item.insertText) &&
      /\s/.test(trimmed.charAt(item.insertText.length))
    ) {
      return { item, args: trimmed.slice(item.insertText.length).trim() };
    }
  }
  return null;
}
