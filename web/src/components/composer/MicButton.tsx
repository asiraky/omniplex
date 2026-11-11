import { MicIcon, SquareIcon, XIcon } from "lucide-react";
import { useRef } from "react";

import { IconButton } from "~/components/IconButton";
import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import { formatElapsed } from "~/lib/dictation";
import { cn } from "~/lib/utils";

import type { DictationStatus } from "./useDictation";

/** A press held at least this long was a hold: letting go sends it. Anything
    shorter was a tap, and the recording runs until the next one. */
export const HOLD_MS = 350;

const label: Record<DictationStatus, string> = {
  idle: "Dictate",
  starting: "Stop and transcribe",
  recording: "Stop and transcribe",
  transcribing: "Transcribing…",
};

/**
 * The composer's mic. One button does both ways of dictating: hold it and
 * talk, and letting go sends; or tap it, talk, and tap again.
 *
 * Pointer events rather than clicks, because a click only says the press
 * ended. Keyboard and assistive tech still arrive as clicks, which toggle.
 */
export function MicButton({
  status,
  disabled,
  onStart,
  onStop,
}: {
  status: DictationStatus;
  disabled: boolean;
  onStart: () => void;
  onStop: () => void;
}) {
  // When the current press began, if it was the press that started recording.
  const pressedAt = useRef<number | null>(null);
  const live = status === "starting" || status === "recording";

  return (
    <Button
      type="button"
      variant={live ? "destructive" : "ghost"}
      size="icon"
      disabled={disabled || status === "transcribing"}
      aria-label={label[status]}
      title={status === "idle" ? "Dictate: tap to start and stop, or hold to talk" : label[status]}
      onPointerDown={(e) => {
        if (e.button > 0) return;
        // Keeps focus where it was: the caret stays put on desktop, and on a
        // phone the keyboard neither closes under the press nor opens after.
        e.preventDefault();
        if (status === "recording") {
          pressedAt.current = null;
          onStop();
          return;
        }
        if (status !== "idle") return;
        // Captured so the release still lands here if the thumb drifts off.
        const el = e.currentTarget;
        if (typeof el.setPointerCapture === "function") el.setPointerCapture(e.pointerId);
        pressedAt.current = Date.now();
        onStart();
      }}
      onPointerUp={() => {
        const at = pressedAt.current;
        pressedAt.current = null;
        // Released while the mic was still being asked for (a permission
        // prompt takes the press with it) counts as a tap: the recording
        // starts and waits for a tap to stop.
        if (at !== null && status === "recording" && Date.now() - at >= HOLD_MS) onStop();
      }}
      // The browser took the gesture over. Keep recording rather than guess;
      // the stop button is right there.
      onPointerCancel={() => {
        pressedAt.current = null;
      }}
      onMouseDown={(e) => e.preventDefault()}
      onClick={(e) => {
        // A pointer's click has a count; the press handlers already acted on it.
        if (e.detail !== 0) return;
        if (status === "idle") onStart();
        else if (status === "recording") onStop();
      }}
      // A long press is a hold, not a request for the text-selection menu.
      onContextMenu={(e) => e.preventDefault()}
      className={cn(
        "size-11 shrink-0 touch-none rounded-full select-none [-webkit-touch-callout:none] md:size-8",
        !live && "text-muted-foreground hover:text-foreground",
      )}
    >
      {status === "transcribing" ? (
        <Spinner aria-hidden />
      ) : live ? (
        <SquareIcon className="size-3.5 fill-current" />
      ) : (
        <MicIcon />
      )}
    </Button>
  );
}

/**
 * What stands in for the model picker while recording: a way to throw the
 * clip away, and a pulsing dot with the time so far, so a recording is never
 * running without it being obvious.
 */
export function RecordingStatus({ elapsed, onDiscard }: { elapsed: number; onDiscard: () => void }) {
  return (
    <div className="flex min-w-0 items-center gap-1">
      <IconButton
        label="Discard recording"
        onClick={onDiscard}
        onMouseDown={(e) => e.preventDefault()}
        className="text-muted-foreground hover:text-foreground rounded-full"
      >
        <XIcon />
      </IconButton>
      <span className="text-destructive flex items-center gap-1.5 px-1 text-sm font-medium tabular-nums">
        <span aria-hidden className="bg-destructive size-2.5 rounded-full motion-safe:animate-pulse" />
        <span role="timer" aria-label={`Recording, ${formatElapsed(elapsed)}`}>
          {formatElapsed(elapsed)}
        </span>
      </span>
    </div>
  );
}
