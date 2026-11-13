import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type KeyboardEvent,
  type RefObject,
} from "react";

import {
  detectComposerTrigger,
  rankComposerItems,
  replaceComposerTrigger,
  type ComposerTrigger,
} from "~/lib/composerItems";
import { typeInto } from "~/lib/typeInto";
import type { ComposerItem } from "~/protocol";
import { useLatest } from "~/useLatest";

import type { ComposerCatalogue } from "./useComposerItems";

const CLIENT_ITEMS: ComposerItem[] = [
  {
    id: "client:model",
    name: "model",
    description: "Switch response model for this thread",
    kind: "command",
    trigger: "/",
    insertText: "/model",
    origin: "built-in",
    behavior: "client-action",
    action: "model",
  },
];

const itemKey = (item: ComposerItem) => `${item.trigger}\0${item.insertText}`;
const CLAIMED = new Set(CLIENT_ITEMS.map(itemKey));

/**
 * The completion menu: where the cursor is, which trigger it sits in, what
 * matches, and what choosing an entry does to the draft.
 */
export function useComposerMenu({
  draft,
  draftRef,
  changeDraft,
  catalogue,
  textareaRef,
  disabled,
  sendDisabled,
  runClientAction,
  onRunComposerAction,
}: {
  draft: string;
  draftRef: RefObject<string>;
  changeDraft: (next: string) => void;
  catalogue: ComposerCatalogue;
  textareaRef: RefObject<HTMLTextAreaElement | null>;
  disabled: boolean;
  sendDisabled: boolean;
  runClientAction: (action: string) => void;
  onRunComposerAction?: (action: string, args: string, invocation: string) => Promise<void>;
}) {
  const [cursor, setCursor] = useState(draft.length);
  const [activeIndex, setActiveIndex] = useState(0);
  const [dismissedTrigger, setDismissedTrigger] = useState("");
  const [focused, setFocused] = useState(false);

  const items = useMemo<ComposerItem[]>(
    () => [...CLIENT_ITEMS, ...catalogue.items.filter((item) => !CLAIMED.has(itemKey(item)))],
    [catalogue.items],
  );

  const focusAt = useCallback(
    (nextCursor: number) => {
      window.requestAnimationFrame(() => {
        const el = textareaRef.current;
        if (!el) return;
        el.focus();
        el.setSelectionRange(nextCursor, nextCursor);
        setCursor(nextCursor);
      });
    },
    [textareaRef],
  );

  const trigger = useMemo(
    () => detectComposerTrigger(draft, cursor, items),
    [draft, cursor, items],
  );

  // Provider catalogues can change while a thread is open. Refresh at the
  // start of each completion interaction; native adapters remain authoritative
  // without making the core subscribe to provider-specific invalidations.
  // The query is read through a ref so typing after the trigger does not
  // re-run this: it is once per trigger opening, not once per keystroke.
  const triggerQuery = useLatest(trigger?.query);
  const { reload } = catalogue;
  useEffect(() => {
    if (triggerQuery.current === "") reload();
  }, [reload, trigger?.trigger, triggerQuery]);
  const triggerKey = trigger
    ? `${trigger.start}:${trigger.end}:${trigger.trigger}:${trigger.query}`
    : "";
  const matches = useMemo(
    () => (trigger ? rankComposerItems(items, trigger) : []),
    [items, trigger],
  );
  // Mid-prompt, a token with nothing to offer is prose — `/tmp` — and a menu
  // saying so would only be in the way of it.
  const open = Boolean(
    trigger &&
    (trigger.leading || matches.length > 0) &&
    triggerKey !== dismissedTrigger &&
    !disabled &&
    focused,
  );

  // A new trigger, or a new query under it, is a new list: start at its top.
  const [indexedTrigger, setIndexedTrigger] = useState(triggerKey);
  if (indexedTrigger !== triggerKey) {
    setIndexedTrigger(triggerKey);
    setActiveIndex(0);
  }

  const dismiss = useCallback(() => setDismissedTrigger(triggerKey), [triggerKey]);

  // Typed in where the browser will take it, so a pick can be undone like a
  // keystroke; the draft then follows from the textarea's own change event.
  // `focus` false leaves a textarea the person has since left alone.
  const replaceTrigger = useCallback(
    (from: string, at: ComposerTrigger, text: string, focus: boolean) => {
      const next = replaceComposerTrigger(from, at, text);
      const el = textareaRef.current;
      const typed =
        !!el &&
        el.value === from &&
        (focus || document.activeElement === el) &&
        typeInto(el, at.start, at.end, text);
      if (!typed) changeDraft(next.value);
      return { ...next, typed };
    },
    [changeDraft, textareaRef],
  );

  const choose = useCallback(
    (item: ComposerItem) => {
      if (!trigger) return;
      if (item.behavior === "client-action" && item.action) {
        replaceTrigger(draft, trigger, "", true);
        setDismissedTrigger(triggerKey);
        runClientAction(item.action);
        return;
      }
      if (item.behavior === "adapter-action" && item.action) {
        // An adapter action is a turn by another name: it goes to the same
        // thread the send button is waiting on, so it waits with it.
        if (sendDisabled) return;
        setDismissedTrigger(triggerKey);
        // Keep the literal command in place if the provider rejects it; App
        // has already surfaced the error and the user can retry or edit it.
        const submittedDraft = draft;
        void onRunComposerAction?.(item.action, "", item.insertText)
          .then(() => {
            if (draftRef.current === submittedDraft) replaceTrigger(submittedDraft, trigger, "", false);
          })
          .catch(() => {});
        return;
      }
      const next = replaceTrigger(draft, trigger, `${item.insertText} `, true);
      setDismissedTrigger(triggerKey);
      if (!next.typed) focusAt(next.cursor);
    },
    [
      draft,
      draftRef,
      focusAt,
      onRunComposerAction,
      replaceTrigger,
      runClientAction,
      sendDisabled,
      trigger,
      triggerKey,
    ],
  );

  /** Takes the keys the open menu owns; true when it took this one. */
  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (!open) return false;
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (matches.length > 0) {
        const offset = e.key === "ArrowDown" ? 1 : -1;
        setActiveIndex((index) => (index + offset + matches.length) % matches.length);
      }
      return true;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      dismiss();
      return true;
    }
    if (e.key === "Enter" || e.key === "Tab") {
      e.preventDefault();
      if (matches.length > 0) {
        choose(matches[Math.min(activeIndex, matches.length - 1)]!);
      }
      return true;
    }
    return false;
  };

  return {
    items,
    matches,
    open,
    activeIndex,
    setActiveIndex,
    setCursor,
    setFocused,
    focusAt,
    choose,
    dismiss,
    onKey,
  };
}
