import { CheckIcon, CopyIcon } from "lucide-react";
import { lazy, Suspense, type RefObject } from "react";

import { useCopy } from "~/lib/clipboard";
import { useReadAloud } from "~/lib/readAloud";
import { useReply } from "~/lib/reply";
import { cn } from "~/lib/utils";
import type { Item } from "~/protocol";

import { ReplyButton } from "./ReplyButton";

const PlayButton = lazy(() => import("./PlayButton"));

function receivedTime(ms?: number): string {
  if (!ms) return "";
  return new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

// About 22px tall, which a thumb misses; on a touch screen `hit-44` grows the
// target to 44px without moving the footer. The rows either side are 14px
// away, so the grown areas reach into the gap but never another control.
const footerButton =
  "hover:text-foreground focus-visible:ring-ring hit-44 relative flex cursor-pointer items-center gap-1 rounded-md px-1 py-0.5 outline-none focus-visible:ring-2";

/**
 * The footer under a message: when it arrived, a one-click copy of the raw
 * text, and on an agent's reply a way to answer it. Quiet by design (metadata
 * should not compete with the prose), so with a mouse it fades in on hover, and
 * it stays small everywhere. A touch screen has no hover to bring it in, so
 * there it is always showing.
 *
 * With read-aloud on it also plays the message, or the reader's selection when
 * that reaches into this message. `speakRef` is the message's rendered content
 * (its `data-speak-root`); without one there is nothing to read and no button.
 * `rawMarkdown` says the content shows markdown source, as a prompt does; it
 * is read as if rendered, so a pasted fence or table reads as a reply's would.
 *
 * `replyable` marks an agent's reply, which can be quoted into the composer to
 * answer it; a prompt cannot. It needs `speakRef` too, to find a selection in.
 */
export function MessageMeta({
  item,
  speakRef,
  rawMarkdown = false,
  replyable = false,
}: {
  item: Item;
  speakRef?: RefObject<HTMLElement | null>;
  rawMarkdown?: boolean;
  replyable?: boolean;
}) {
  const { copied, copy } = useCopy();
  const readAloud = useReadAloud();
  const reply = useReply();
  const canSpeak = !!readAloud && !!speakRef && !!item.text;
  const time = receivedTime(item.receivedAt);
  if (!time && !item.text) return null;

  return (
    <div
      className={cn(
        "text-muted-foreground flex items-center gap-1.5 text-[13px] opacity-100 transition-opacity mouse:opacity-0 mouse:group-hover:opacity-100 mouse:group-focus-within:opacity-100",
        // Hover-only would hide the very controls in use: the selection
        // indicator, and the stop button once the pointer moves away.
        "mouse:has-data-active:opacity-100",
      )}
    >
      {time && <span className="font-mono">{time}</span>}
      <button
        type="button"
        onClick={() => void copy(item.text ?? "")}
        aria-label="Copy message"
        className={footerButton}
      >
        {copied ? (
          <CheckIcon className="text-success size-3.5" />
        ) : (
          <CopyIcon className="size-3.5" />
        )}
        {copied ? "copied" : "copy"}
      </button>
      {replyable && reply && speakRef && item.text && (
        <ReplyButton
          text={item.text}
          contentRef={speakRef}
          onReply={reply}
          className={footerButton}
        />
      )}
      {canSpeak && (
        <Suspense fallback={null}>
          <PlayButton
            id={item.id}
            speakRef={speakRef}
            rawMarkdown={rawMarkdown}
            readAloud={readAloud}
            className={footerButton}
          />
        </Suspense>
      )}
    </div>
  );
}
