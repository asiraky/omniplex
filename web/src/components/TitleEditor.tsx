import { useEffect, useRef } from "react";

import { cn } from "~/lib/utils";

/** The server caps a typed title at this many characters. */
const MAX_TITLE = 200;

/** True for the field TitleEditor renders, wherever it is on the page. */
export function isTitleEditor(el: Element | null) {
  return el instanceof HTMLElement && el.dataset.titleEditor !== undefined;
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

/**
 * A thread title, edited where it is shown. Enter or clicking away saves it.
 * Escape keeps the old one. The field mounts focused with its text selected,
 * so typing replaces the name and an arrow key starts editing it.
 */
export function TitleEditor({
  title,
  label,
  onSave,
  onDone,
  className,
}: {
  title: string;
  /** The field's accessible name. */
  label: string;
  onSave: (title: string) => void;
  /** Called once when the edit ends, saved or not. */
  onDone: () => void;
  className?: string;
}) {
  const ref = useRef<HTMLInputElement>(null);
  // Enter saves and unmounts the field, and unmounting it blurs it. Without
  // this guard the blur would finish the same edit a second time.
  const finished = useRef(false);
  const finish = (save: boolean) => {
    if (finished.current) return;
    finished.current = true;
    const next = save ? titleToSave(ref.current?.value ?? "", title) : null;
    if (next !== null) onSave(next);
    onDone();
  };

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.focus();
    el.select();
  }, []);

  return (
    <input
      ref={ref}
      data-title-editor=""
      aria-label={label}
      defaultValue={title}
      maxLength={MAX_TITLE}
      enterKeyHint="done"
      autoComplete="off"
      spellCheck={false}
      onBlur={() => finish(true)}
      onKeyDown={(e) => {
        if (e.key === "Enter" && !e.nativeEvent.isComposing) {
          e.preventDefault();
          finish(true);
        } else if (e.key === "Escape") {
          e.preventDefault();
          e.stopPropagation();
          finish(false);
        }
      }}
      // 16px on a phone: iOS zooms the page into any smaller field it focuses.
      className={cn(
        "bg-background ring-ring/60 min-w-0 rounded-sm px-1 text-[16px] ring-1 outline-none md:text-[13px]",
        className,
      )}
    />
  );
}
