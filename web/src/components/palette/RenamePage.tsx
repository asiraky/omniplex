import { useEffect, useRef } from "react";

import { isComposing } from "~/lib/ime";
import { titleToSave } from "~/lib/threadTitle";
import { cn } from "~/lib/utils";

/** The server caps a title at this many characters: maxTitleRunes in internal/thread. */
const MAX_TITLE = 200;

/**
 * Renaming the open thread, in the palette's own search field. Enter saves
 * and closes; Escape, or Backspace once the field is empty, goes back to the
 * list without saving. A blank or untouched name saves nothing, as in the
 * header's editor.
 */
export function RenamePage({
  title,
  touch,
  onSave,
  onBack,
}: {
  title: string;
  touch: boolean;
  /** Called with the new name, or null when there is nothing to save. */
  onSave: (title: string | null) => void;
  onBack: () => void;
}) {
  const ref = useRef<HTMLInputElement>(null);

  useEffect(() => {
    ref.current?.focus();
    ref.current?.select();
  }, []);

  return (
    <div className="flex flex-col">
      <div className={cn("flex items-center border-b px-3", touch ? "h-12 pr-12" : "h-11")}>
        <input
          ref={ref}
          aria-label="Thread title"
          defaultValue={title}
          maxLength={MAX_TITLE}
          enterKeyHint="done"
          autoComplete="off"
          spellCheck={false}
          onKeyDown={(e) => {
            if (isComposing(e)) return;
            if (e.key === "Enter") {
              e.preventDefault();
              onSave(titleToSave(e.currentTarget.value, title));
            } else if (e.key === "Backspace" && e.currentTarget.value === "") {
              e.preventDefault();
              onBack();
            }
          }}
          // 16px on touch screens, or iOS zooms the page on focus.
          className={cn(
            "w-full bg-transparent outline-hidden placeholder:text-muted-foreground",
            touch ? "text-[16px]" : "text-sm",
          )}
          placeholder="Thread title"
        />
      </div>
      {!touch && (
        <p className="text-muted-foreground px-3 py-2 text-xs">Enter saves. Escape goes back.</p>
      )}
    </div>
  );
}
