import { useEffect, useMemo, useRef, type RefObject } from "react";

import { parseRefs, type RefMatch } from "~/lib/composerRefs";
import { cn } from "~/lib/utils";

/**
 * The pills painted over the composer's file tokens.
 *
 * The input stays a real `<textarea>` — caret, selection, undo, autocorrect,
 * IME and every phone keyboard quirk are the browser's problem, not ours,
 * which is the whole reason a contenteditable rewrite was not worth it. What
 * this does instead is lay a second copy of the same string exactly on top of
 * it, with every glyph transparent, and give the `@path` runs a background.
 * The visible text is still the textarea's, underneath; only the pill is ours.
 *
 * That ordering is load-bearing. Rendering the *text* here and hiding the
 * textarea's would put the native selection highlight above the words and make
 * them unreadable while selected. Painting only backgrounds keeps selection,
 * spellcheck squiggles and the caret exactly as the platform draws them.
 *
 * Metrics have to match to the pixel or the pills drift off their words, so
 * every typographic property is shared with the textarea and the pill's
 * padding is cancelled by an equal negative margin — visually a pill, in
 * layout terms still just the characters it covers.
 */
export function ComposerMirror({
  textareaRef,
  anchorRef,
  text,
  onRemove,
}: {
  textareaRef: RefObject<HTMLTextAreaElement | null>;
  /** The positioned box both this and the textarea are laid out inside. */
  anchorRef: RefObject<HTMLElement | null>;
  text: string;
  onRemove: (ref: RefMatch) => void;
}) {
  const mirrorRef = useRef<HTMLDivElement>(null);
  const refs = useMemo(() => parseRefs(text), [text]);

  // Sit exactly on the textarea's padding box. clientWidth/Height exclude the
  // border and any scrollbar the textarea is showing, so the mirror wraps its
  // lines at the same column the textarea does even on platforms whose
  // scrollbars take up room.
  useEffect(() => {
    const el = textareaRef.current;
    const mirror = mirrorRef.current;
    const anchor = anchorRef.current;
    if (!el || !mirror || !anchor) return;
    const place = () => {
      mirror.style.left = `${el.offsetLeft}px`;
      mirror.style.top = `${el.offsetTop}px`;
      mirror.style.width = `${el.clientWidth}px`;
      mirror.style.height = `${el.clientHeight}px`;
      mirror.scrollTop = el.scrollTop;
    };
    place();
    // The textarea grows with the draft and the card moves when the chip strip
    // appears above it; both change where the pills belong.
    const observer = new ResizeObserver(place);
    observer.observe(el);
    observer.observe(anchor);
    return () => observer.disconnect();
  }, [textareaRef, anchorRef, text]);

  // A long draft scrolls inside the textarea; the pills scroll with it.
  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    const sync = () => {
      if (mirrorRef.current) mirrorRef.current.scrollTop = el.scrollTop;
    };
    el.addEventListener("scroll", sync, { passive: true });
    return () => el.removeEventListener("scroll", sync);
  }, [textareaRef]);

  const segments = useMemo(() => {
    const out: Array<string | RefMatch> = [];
    let at = 0;
    for (const ref of refs) {
      if (ref.start > at) out.push(text.slice(at, ref.start));
      out.push(ref);
      at = ref.end;
    }
    out.push(text.slice(at));
    return out;
  }, [text, refs]);

  return (
    <div
      ref={mirrorRef}
      aria-hidden
      // The text is a positioning scaffold, never read: it must not be
      // selectable (it would fight the textarea's own selection) and must not
      // be findable by the browser's find-in-page as a phantom second copy.
      className={cn(
        "pointer-events-none absolute z-10 overflow-hidden text-transparent select-none",
        // Every one of these is copied from the textarea. Changing the
        // textarea's typography without changing this too misaligns the pills.
        "px-4 pt-3 pb-1 text-[16px] leading-relaxed break-words whitespace-pre-wrap md:text-[14px]",
      )}
    >
      {segments.map((segment, i) =>
        typeof segment === "string" ? (
          <span key={i}>{segment}</span>
        ) : (
          <span
            key={i}
            // pointer-events come back on for the pill alone, so it can be
            // clicked away. Everywhere else the click has to reach the
            // textarea and place a caret.
            // -mx-[4px] is not a nudge: it cancels the 3px padding plus the
            // 1px border exactly, so the pill occupies the same advance width
            // as the bare characters and the rest of the line stays aligned.
            className="group border-primary/25 bg-primary/10 hover:border-destructive/40 hover:bg-destructive/15 pointer-events-auto relative -mx-[4px] cursor-pointer rounded-[5px] border px-[3px] [-webkit-box-decoration-break:clone] [box-decoration-break:clone]"
            title={`${segment.path}${segment.from ? ` (from line ${segment.from})` : ""} — click to remove`}
            // Removing a chip must not steal focus out of the message the
            // reader is in the middle of writing.
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => onRemove(segment)}
          >
            {segment.text}
            <span className="text-destructive pointer-events-none absolute -top-1.5 -right-1.5 hidden size-3.5 place-items-center rounded-full border bg-card text-[9px] leading-none group-hover:grid">
              ✕
            </span>
          </span>
        ),
      )}
    </div>
  );
}
