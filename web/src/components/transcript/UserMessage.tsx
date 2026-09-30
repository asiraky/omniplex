import { useLayoutEffect, useMemo, useRef, useState } from "react";

import { parseAttachedFiles, type Artefact } from "~/lib/artefacts";
import { cn } from "~/lib/utils";
import type { Item } from "~/protocol";

import { PromptFiles, PromptImages, type OpenArtefact } from "./Attachments";
import { MessageMeta } from "./MessageMeta";

// A user message longer than this many lines collapses behind a fade until the
// reader opens it. The clamp is a real height, not a character count: a single
// long wrapped paste collapses just as a hundred hard breaks would, and a short
// message is judged by how tall it actually renders — so nothing that already
// fits ever grows a button.
const MAX_COLLAPSED_USER_MESSAGE_LINES = 12;
// The fade eats the last line or so, enough to read as "there's more" without
// swallowing a whole line of text.
const COLLAPSED_USER_MESSAGE_FADE = "1.75rem";
const COLLAPSED_USER_MESSAGE_MASK = `linear-gradient(to bottom, black calc(100% - ${COLLAPSED_USER_MESSAGE_FADE}), transparent)`;

// The user's own prompt, which — unlike everything else in the transcript — can
// be an arbitrarily large paste. Left alone it renders at full height forever
// and buries the conversation under the reader's own text, so past the clamp we
// hide the overflow behind a soft fade and a toggle. The fade is a CSS mask, not
// an overlay: it needs no knowledge of the bubble's colour, so it works in both
// themes for free. Expanded state is per-message and never persisted —
// reopening the thread starts collapsed again.
export function UserMessage({
  item,
  threadId,
  artefacts,
  onOpenArtefact,
}: {
  item: Item;
  threadId: string;
  artefacts: Artefact[];
  onOpenArtefact?: OpenArtefact;
}) {
  const { text, files } = useMemo(() => parseAttachedFiles(item.text ?? ""), [item.text]);
  const [expanded, setExpanded] = useState(false);
  const [overflowing, setOverflowing] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);

  // Measure the overflow rather than counting characters: the same text is a
  // very different height depending on how it wraps. The clamp is applied
  // whenever the bubble isn't expanded (see `clamped` below), so while
  // collapsed a scrollHeight past the clamp is the real signal there's more.
  // Measuring against the clamped element is what makes this work — measure an
  // unconstrained element and its scrollHeight and clientHeight always agree,
  // so nothing would ever look overflowing. Skip the measure while expanded:
  // the clamp is off, the two heights agree, and re-measuring would only
  // wrongly clear the toggle.
  useLayoutEffect(() => {
    if (expanded) return;
    const el = bodyRef.current;
    if (!el) return;
    const measure = () => setOverflowing(el.scrollHeight - el.clientHeight > 1);
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [text, expanded]);

  // Clamp whenever not expanded — including before the first measurement — so
  // the measurement above runs against a constrained element. A short message
  // is shorter than the clamp, so the cap does nothing visible to it; only a
  // message that actually overflows gets the fade and the toggle.
  const clamped = !expanded;

  const toggle = () => {
    // Collapsing can leave the bubble's top scrolled off above the viewport;
    // bring it back so the transcript doesn't land somewhere random.
    if (expanded)
      requestAnimationFrame(() => wrapRef.current?.scrollIntoView({ block: "nearest" }));
    setExpanded((v) => !v);
  };

  return (
    <div ref={wrapRef} data-msg-id={item.id} className="group fade-in flex flex-col items-end">
      {item.images && item.images.length > 0 && (
        <PromptImages threadId={threadId} images={item.images} />
      )}
      {files.length > 0 && (
        <PromptFiles files={files} artefacts={artefacts} onOpen={onOpenArtefact} />
      )}
      {/* An attachment-only message has no bubble to draw: an empty one reads
          as a message that failed to arrive. */}
      {(text || (!item.images?.length && files.length === 0)) && (
        <div
          ref={bodyRef}
          style={
            clamped
              ? {
                  maxHeight: `${MAX_COLLAPSED_USER_MESSAGE_LINES}lh`,
                  ...(overflowing && {
                    WebkitMaskImage: COLLAPSED_USER_MESSAGE_MASK,
                    maskImage: COLLAPSED_USER_MESSAGE_MASK,
                  }),
                }
              : undefined
          }
          className={cn(
            "bg-user-bubble text-user-bubble-foreground max-w-[85%] rounded-2xl rounded-br-md px-3.5 py-2 text-[14px] leading-relaxed break-words whitespace-pre-wrap",
            clamped && "overflow-hidden",
          )}
        >
          {text}
        </div>
      )}
      {overflowing && (
        <button
          type="button"
          onClick={toggle}
          aria-expanded={expanded}
          className="text-muted-foreground hover:text-foreground focus-visible:ring-ring mt-1 rounded-sm px-1 text-[12px] transition-colors outline-none focus-visible:ring-2"
        >
          {expanded ? "Show less" : "Show more"}
        </button>
      )}
      <MessageMeta item={item} />
    </div>
  );
}
