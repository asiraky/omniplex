import { ChevronDownIcon, ChevronUpIcon, XIcon } from "lucide-react";
import { useEffect, useRef, useState, type KeyboardEvent } from "react";

import { IconButton } from "~/components/IconButton";
import { Input } from "~/components/ui/input";
import type { ThreadFindControls, ThreadMatch } from "~/lib/threadFind";
import { cn } from "~/lib/utils";
import { useLatest } from "~/useLatest";

import { useThreadFind, type ThreadFind } from "./useThreadFind";

// Nothing while there is nothing to count. A re-run (stepping past the newest
// match) keeps the old count up, dimmed, rather than blinking it out.
function counter(f: ThreadFind, jumpFailed: boolean): string {
  if (f.status === "error") return "Search failed";
  if (jumpFailed) return "Couldn't load match";
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
 * it, and resolves false when the match could not be loaded; `onClear` hears
 * that whatever was painted is stale (the query changed, or the bar closed).
 * `cuts` moves when a rewind cuts the timeline back, which takes matches with
 * it, so the query runs again.
 */
export function ThreadFindBar({
  controls,
  cuts,
  onJump,
  onClear,
}: {
  controls: ThreadFindControls;
  cuts: number;
  onJump: (match: ThreadMatch, query: string) => Promise<boolean>;
  onClear: () => void;
}) {
  const f = useThreadFind(controls.search);
  const inputRef = useRef<HTMLInputElement>(null);
  const onJumpRef = useLatest(onJump);
  const onClearRef = useLatest(onClear);
  const [jumpFailed, setJumpFailed] = useState(false);

  // Focus goes back where it was when the bar closes, not to the page body.
  useEffect(() => {
    const before = document.activeElement;
    return () => {
      const lost = !document.activeElement || document.activeElement === document.body;
      if (lost && before instanceof HTMLElement && before.isConnected) before.focus();
    };
  }, []);

  // On open, and again on every repeat ask to open, so a second Ctrl/Cmd+F
  // lands back in the field with its text ready to type over.
  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    input.focus();
    input.select();
  }, [controls.nonce]);

  // The painted occurrences belong to the query that found them.
  const typed = (q: string) => {
    onClear();
    setJumpFailed(false);
    f.setQuery(q);
  };
  useEffect(() => () => onClearRef.current(), [onClearRef]);

  // The snapshot holds matches the rewind took; stepping onto one would go
  // nowhere. Only a cut since the bar opened counts.
  const rerun = useLatest(f.rerun);
  const openedAt = useRef(cuts);
  useEffect(() => {
    if (cuts === openedAt.current) return;
    openedAt.current = cuts;
    onClearRef.current();
    setJumpFailed(false);
    rerun.current();
  }, [cuts, rerun, onClearRef]);

  // `step` moves on every selection, including a re-selection of the same
  // match, which is a jump all the same.
  const current = f.current;
  const query = f.query;
  useEffect(() => {
    if (!current) return;
    setJumpFailed(false);
    let live = true;
    void onJumpRef.current(current, query).then((ok) => live && !ok && setJumpFailed(true));
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [f.step]);

  const stepping = f.status === "ready" && f.total > 0;
  const onInputKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key !== "Enter") return;
    e.preventDefault();
    if (f.status === "error") f.rerun();
    else if (e.shiftKey) f.prev();
    else f.next();
  };
  // On the whole bar, so Escape closes it from the step buttons too.
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Escape") {
      // Escape elsewhere can mean "close the panel" or "stop the turn"; here
      // it means this bar and nothing else.
      e.preventDefault();
      e.stopPropagation();
      controls.close();
    }
  };

  return (
    <div
      onKeyDown={onKeyDown}
      className="bg-background flex shrink-0 items-center gap-1 border-b px-2 py-1 md:px-3"
    >
      <Input
        ref={inputRef}
        type="search"
        aria-label="Find in thread"
        placeholder="Find in thread"
        value={f.query}
        onChange={(e) => typed(e.target.value)}
        onKeyDown={onInputKeyDown}
        enterKeyHint="search"
        autoComplete="off"
        className="h-11 min-w-0 flex-1 text-[13px] md:h-8"
      />
      <span
        aria-live="polite"
        className={cn(
          "shrink-0 px-1 text-center text-[11px] whitespace-nowrap tabular-nums",
          f.status === "error" || jumpFailed ? "text-destructive" : "text-muted-foreground",
          f.status === "loading" && "opacity-60",
        )}
      >
        {counter(f, jumpFailed)}
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
