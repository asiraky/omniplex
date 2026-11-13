import { ChevronDownIcon, ChevronUpIcon, XIcon } from "lucide-react";
import { useEffect, useRef, type KeyboardEvent } from "react";

import { IconButton } from "~/components/IconButton";
import { Input } from "~/components/ui/input";
import type { ThreadFindControls, ThreadMatch } from "~/lib/threadFind";
import { cn } from "~/lib/utils";
import { useLatest } from "~/useLatest";

import { useThreadFind, type ThreadFind } from "./useThreadFind";

// Nothing while there is nothing to count. A re-run (stepping past the newest
// match) keeps the old count up, dimmed, rather than blinking it out.
function counter(f: ThreadFind): string {
  if (f.status === "error") return "Search failed";
  if (f.status === "loading") return f.current ? `${f.ordinal} of ${f.total}` : "…";
  if (f.status !== "ready") return "";
  if (f.total === 0) return "No matches";
  return `${f.ordinal} of ${f.total}`;
}

/**
 * Find in thread, docked over the top of the transcript: it sits above the
 * scroller rather than over it, so it can never cover the match it jumped to.
 * Mounted only while open, so a closed bar runs no searches and closing it
 * throws the query away.
 *
 * `onJump` hears every (re)selection of a match, with the query to paint in
 * it; `onClear` hears that whatever was painted is stale (the query changed,
 * or the bar closed).
 */
export function ThreadFindBar({
  controls,
  onJump,
  onClear,
}: {
  controls: ThreadFindControls;
  onJump: (match: ThreadMatch, query: string) => void;
  onClear: () => void;
}) {
  const f = useThreadFind(controls.search);
  const inputRef = useRef<HTMLInputElement>(null);
  const onJumpRef = useLatest(onJump);
  const onClearRef = useLatest(onClear);

  // On open, and again on every repeat ask to open, so a second Ctrl/Cmd+F
  // lands back in the field with its text ready to type over.
  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    input.focus();
    input.select();
  }, [controls.nonce]);

  // The painted occurrences belong to the query that found them.
  useEffect(() => {
    onClearRef.current();
  }, [f.query, onClearRef]);
  useEffect(() => () => onClearRef.current(), [onClearRef]);

  // `step` moves on every selection, including a re-selection of the same
  // match, which is a jump all the same.
  const current = f.current;
  const query = f.query;
  useEffect(() => {
    if (current) onJumpRef.current(current, query);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [f.step]);

  const stepping = f.status === "ready" && f.total > 0;
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") {
      e.preventDefault();
      if (f.status === "error") f.rerun();
      else if (e.shiftKey) f.prev();
      else f.next();
    } else if (e.key === "Escape") {
      // Escape elsewhere can mean "close the panel" or "stop the turn"; here
      // it means this bar and nothing else.
      e.preventDefault();
      e.stopPropagation();
      controls.close();
    }
  };

  return (
    <div className="bg-background flex shrink-0 items-center gap-1 border-b px-2 py-1 md:px-3">
      <Input
        ref={inputRef}
        type="search"
        aria-label="Find in thread"
        placeholder="Find in thread"
        value={f.query}
        onChange={(e) => f.setQuery(e.target.value)}
        onKeyDown={onKeyDown}
        enterKeyHint="search"
        autoComplete="off"
        className="h-11 min-w-0 flex-1 text-[13px] md:h-8"
      />
      <span
        aria-live="polite"
        className={cn(
          "shrink-0 px-1 text-center text-[11px] whitespace-nowrap tabular-nums",
          f.status === "error" ? "text-destructive" : "text-muted-foreground",
          f.status === "loading" && "opacity-60",
        )}
      >
        {counter(f)}
      </span>
      <IconButton label="Previous match" disabled={!stepping} onClick={() => f.prev()}>
        <ChevronUpIcon />
      </IconButton>
      <IconButton label="Next match" disabled={!stepping} onClick={() => f.next()}>
        <ChevronDownIcon />
      </IconButton>
      <IconButton label="Close find" onClick={controls.close}>
        <XIcon />
      </IconButton>
    </div>
  );
}
