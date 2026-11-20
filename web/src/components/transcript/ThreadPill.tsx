import { MessagesSquareIcon } from "lucide-react";
import { useContext } from "react";

import { OpenThreadContext, type ThreadMention } from "~/lib/threadMentions";
import { cn } from "~/lib/utils";

/**
 * A thread a message names, as a pill rather than its raw link. Tapping it
 * opens the thread; where nothing can open one it is only a label.
 */
export function ThreadPill({ mention: m, className }: { mention: ThreadMention; className?: string }) {
  const open = useContext(OpenThreadContext);
  const title = m.title || "Untitled";
  const look = cn(
    "inline-flex max-w-full min-w-0 items-center gap-1 rounded-full border px-2 align-middle text-[12px] leading-6 whitespace-nowrap",
    className,
  );
  const body = (
    <>
      <MessagesSquareIcon aria-hidden className="size-3 shrink-0 opacity-70" />
      <span className="min-w-0 truncate">{title}</span>
    </>
  );
  if (!open) return <span className={look}>{body}</span>;
  return (
    <button
      type="button"
      onClick={() => open(m.id)}
      aria-label={`Open thread ${title}`}
      className={cn(look, "hover:bg-accent/60 focus-visible:ring-ring cursor-pointer outline-none focus-visible:ring-2")}
    >
      {body}
    </button>
  );
}
