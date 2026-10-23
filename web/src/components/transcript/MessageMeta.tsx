import { CheckIcon, CopyIcon, SquareIcon, TextSelectIcon, Volume2Icon } from "lucide-react";
import { useRef, type RefObject } from "react";

import { Spinner } from "~/components/ui/spinner";
import { useCopy } from "~/lib/clipboard";
import { useReadAloud, type ReadAloud } from "~/lib/readAloud";
import { speakableSelection, speakableText } from "~/lib/speech";
import { currentSelection, selects, useSelects } from "~/lib/speechSelection";
import { toast } from "~/lib/toast";
import { player, usePlayback, type PlaybackStatus } from "~/lib/ttsPlayer";
import { cn } from "~/lib/utils";
import type { Item } from "~/protocol";

function receivedTime(ms?: number): string {
  if (!ms) return "";
  return new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

const footerButton =
  "hover:text-foreground focus-visible:ring-ring flex cursor-pointer items-center gap-1 rounded-md px-1 py-0.5 outline-none focus-visible:ring-2";

/**
 * The footer under a message: when it arrived, and a one-click copy of
 * the raw text. Quiet by design — metadata should not compete with the prose —
 * so it fades in on hover on a desktop and stays small everywhere.
 *
 * With read-aloud on it also plays the message, or the reader's selection when
 * that reaches into this message. `speakRef` is the message's rendered content
 * (its `data-speak-root`); without one there is nothing to read and no button.
 * `speakAll` is what the whole message reads as, when that is not what is
 * rendered.
 */
export function MessageMeta({
  item,
  speakRef,
  speakAll,
}: {
  item: Item;
  speakRef?: RefObject<HTMLElement | null>;
  speakAll?: () => string;
}) {
  const { copied, copy } = useCopy();
  const readAloud = useReadAloud();
  const canSpeak = !!readAloud && !!speakRef && !!item.text;
  const playback = usePlayback(item.id);
  const selected = useSelects(speakRef, canSpeak);
  const time = receivedTime(item.receivedAt);
  if (!time && !item.text) return null;

  return (
    <div
      className={cn(
        "text-muted-foreground flex items-center gap-1.5 text-[13px] opacity-100 transition-opacity md:opacity-0 md:group-hover:opacity-100 md:group-focus-within:opacity-100",
        // Hover-only would hide the very controls in use: the selection
        // indicator, and the stop button once the pointer moves away.
        (selected || playback) && "md:opacity-100",
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
      {canSpeak && (
        <PlayButton
          id={item.id}
          speakRef={speakRef}
          speakAll={speakAll}
          readAloud={readAloud}
          playback={playback}
          selected={selected}
        />
      )}
    </div>
  );
}

const LOOKS = {
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

function PlayButton({
  id,
  speakRef,
  speakAll,
  readAloud,
  playback,
  selected,
}: {
  id: string;
  speakRef: RefObject<HTMLElement | null>;
  speakAll?: () => string;
  readAloud: ReadAloud;
  playback: PlaybackStatus;
  selected: boolean;
}) {
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
      ? speakableSelection(range!)
      : (speakAll?.() ?? speakableText(root));
    if (!text.trim()) {
      toast.info("There is nothing here to read aloud.");
      return;
    }
    player.play(id, text, readAloud.threadId, readAloud.title);
  };

  const look = LOOKS[playback || (selected ? "selection" : "idle")];

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
      className={cn(
        footerButton,
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
