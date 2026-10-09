import type { ThreadMeta, ThreadState } from "~/protocol";

/**
 * A thread's name. The list entry comes first because that is where a rename
 * lands; the attached state never hears about one. The state's title covers a
 * new thread whose list entry has not caught up with its first prompt yet.
 */
export function threadTitle(meta: ThreadMeta | undefined, state: ThreadState | null) {
  return meta?.title || state?.title || "";
}

/**
 * Decides what a finished edit amounts to: the title to save, or null when
 * there is nothing to save. A blank field and an untouched one both mean
 * "keep the old name". Saving a blank would leave a thread with no name.
 */
export function titleToSave(draft: string, current: string) {
  const next = draft.replace(/\s+/g, " ").trim();
  if (!next || next === current.trim()) return null;
  return next;
}

/** True for the field TitleEditor renders, wherever it is on the page. */
export function isTitleEditor(el: Element | null) {
  return el instanceof HTMLElement && el.dataset.titleEditor !== undefined;
}
