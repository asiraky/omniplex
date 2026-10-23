import { SquareIcon, TextSelectIcon, Volume2Icon } from "lucide-react";
import { useRef, type RefObject } from "react";

import { Spinner } from "~/components/ui/spinner";
import type { ReadAloud } from "~/lib/readAloud";
import { speakableMarkdown } from "~/lib/speakMarkdown";
import { speakableSelection, speakableText } from "~/lib/speech";
import { currentSelection, selects, useSelects } from "~/lib/speechSelection";
import { toast } from "~/lib/toast";
import { player, usePlayback } from "~/lib/ttsPlayer";
import { cn } from "~/lib/utils";

/** A selection inside markdown source is that source's slice, read as rendered. */
function selectionText(range: Range, root: HTMLElement, rawMarkdown: boolean): string {
  return rawMarkdown && root.contains(range.commonAncestorContainer)
    ? speakableMarkdown(range.toString())
    : speakableSelection(range);
}

const PLAY_STATES = {
  loading: {
    icon: <Spinner className="size-3.5" aria-hidden role={undefined} />,
    label: "stop",
    aria: "Stop reading aloud",
  },
  playing: {
    icon: <SquareIcon className="size-3 fill-current" />,
    label: "stop",
    aria: "Stop reading aloud",
  },
  selection: {
    icon: <TextSelectIcon className="size-3.5" />,
    label: "play selection",
    aria: "Read the selection aloud",
  },
  idle: { icon: <Volume2Icon className="size-3.5" />, label: "play", aria: "Read aloud" },
};

/**
 * Plays the message, or the reader's selection when that reaches into it. Its
 * own chunk, with the converter and the player: the page loads it only once
 * there is a key, and it is in by the time anyone reaches for it.
 *
 * Marked `data-active` while it is showing a selection or playing, so the
 * footer can stay visible without hover.
 */
export default function PlayButton({
  id,
  speakRef,
  rawMarkdown,
  readAloud,
  className,
}: {
  id: string;
  speakRef: RefObject<HTMLElement | null>;
  rawMarkdown: boolean;
  readAloud: ReadAloud;
  className: string;
}) {
  const playback = usePlayback(id);
  const selected = useSelects(speakRef, true);
  // A phone can drop the selection as the tap lands, before the click fires;
  // what was selected when the finger went down is what the reader meant.
  const pressed = useRef<Range | null>(null);

  const start = () => {
    const range = pressed.current ?? currentSelection();
    pressed.current = null;
    const root = speakRef.current;
    if (!root) return;
    // The whole selection, even where it runs on into other messages: this
    // message's button is just where the reader reached for it.
    const text = selects(range, root)
      ? selectionText(range!, root, rawMarkdown)
      : rawMarkdown
        ? speakableMarkdown(root.textContent ?? "")
        : speakableText(root);
    if (!text.trim()) {
      toast.info("There is nothing here to read aloud.");
      return;
    }
    player.play(id, text, readAloud.threadId, readAloud.title);
  };

  const look = PLAY_STATES[playback || (selected ? "selection" : "idle")];

  return (
    <button
      type="button"
      onPointerDown={() => {
        pressed.current = currentSelection();
      }}
      // Focusing the button would clear a desktop selection before the click.
      onMouseDown={(e) => e.preventDefault()}
      onClick={() => (playback ? player.stop() : start())}
      aria-label={look.aria}
      data-active={selected || playback ? "" : undefined}
      className={cn(
        className,
        // A wider hit area for a thumb, without moving the footer's layout.
        "relative after:absolute after:-inset-x-1 after:-inset-y-2.5 md:after:hidden",
        selected && !playback && "text-primary hover:text-primary",
      )}
    >
      {look.icon}
      {look.label}
    </button>
  );
}
