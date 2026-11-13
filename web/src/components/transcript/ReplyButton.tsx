import { ReplyIcon, TextQuoteIcon } from "lucide-react";
import type { RefObject } from "react";

import { renderedText } from "~/lib/reply";
import { currentSelection, useSelects, within } from "~/lib/speechSelection";
import { cn } from "~/lib/utils";
import { useCanHover } from "~/useMediaQuery";

/**
 * Quotes the reply into the composer to answer it: the whole message, or with
 * a mouse the part of the reader's selection inside it. Unlike play, a
 * selection running on into the next message is cut at this one's edge — the
 * quote is of what this message said. A phone always quotes the whole
 * message: selecting there fights the OS's own menu.
 *
 * Marked `data-active` while it is showing a selection, so the footer stays
 * visible without hover.
 */
export function ReplyButton({
  text,
  contentRef,
  onReply,
  className,
}: {
  /** The message as written, markdown and all: what a whole-message quote carries. */
  text: string;
  contentRef: RefObject<HTMLElement | null>;
  onReply: (quote: string) => void;
  className: string;
}) {
  const mouse = useCanHover();
  const selected = useSelects(contentRef, mouse);

  const reply = () => {
    const selection = mouse ? within(currentSelection(), contentRef.current) : null;
    const part = selection ? renderedText(selection) : "";
    if (part) document.getSelection()?.removeAllRanges();
    onReply(part || text);
  };

  return (
    <button
      type="button"
      // Focusing the button would clear a desktop selection before the click.
      onMouseDown={(e) => e.preventDefault()}
      onClick={reply}
      aria-label={selected ? "Reply quoting the selection" : "Reply quoting this message"}
      data-active={selected ? "" : undefined}
      className={cn(className, selected && "text-primary hover:text-primary")}
    >
      {selected ? <TextQuoteIcon className="size-3.5" /> : <ReplyIcon className="size-3.5" />}
      {selected ? "reply to selection" : "reply"}
    </button>
  );
}
