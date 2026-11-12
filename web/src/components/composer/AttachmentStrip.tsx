import { RotateCwIcon, XIcon } from "lucide-react";
import { useId } from "react";

import { ArtefactTile } from "~/components/artefacts/ArtefactTile";
import { Spinner } from "~/components/ui/spinner";
import type { Attachment } from "~/lib/attachments";
import { cn } from "~/lib/utils";

/**
 * What is going out with the next message: pictures as thumbnails, anything
 * else as a file tile. Sized for a thumb: the remove button is always
 * visible, because there is no hover on a phone, and on touch its 24px circle
 * takes taps across 44px. Where that reaches the next tile, the tile wins: it
 * is later in the strip, so it sits on top, and it does nothing when tapped.
 *
 * A failed upload is retried by tapping the tile itself, which is the biggest
 * target there is on a phone; the remove button stays on top of it.
 */
export function AttachmentStrip({
  attachments,
  tooMuch,
  onRemove,
  onRetry,
}: {
  attachments: Attachment[];
  tooMuch: boolean;
  onRemove?: (key: string) => void;
  onRetry?: (key: string) => void;
}) {
  if (attachments.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-2 px-3 pt-3">
      {attachments.map((a) => (
        <div key={a.key} className={cn("relative", a.kind === "file" && "max-w-60 min-w-0")}>
          {a.kind === "file" ? (
            <FileChip attachment={a} onRetry={onRetry} />
          ) : (
            <ImageThumb attachment={a} onRetry={onRetry} />
          )}
          <button
            type="button"
            onClick={() => onRemove?.(a.key)}
            aria-label={`Remove ${a.name}`}
            className="bg-background text-muted-foreground hover:text-foreground hit-44 absolute -top-2 -right-2 grid size-6 place-items-center rounded-full border shadow-sm"
          >
            <XIcon className="size-3.5" />
          </button>
        </div>
      ))}
      {tooMuch && (
        <p className="text-destructive w-full text-xs">
          Images and PDFs are over 20 MB in total. Remove something to send.
        </p>
      )}
    </div>
  );
}

function ImageThumb({
  attachment: a,
  onRetry,
}: {
  attachment: Attachment;
  onRetry?: (key: string) => void;
}) {
  const errorId = useId();
  const error = a.error ?? "Upload failed";
  return (
    <>
      <img
        src={a.previewUrl}
        alt={a.name}
        className={cn(
          "size-16 rounded-lg border object-cover",
          a.status === "error" && "opacity-40",
        )}
      />
      {a.status === "uploading" && (
        <span className="bg-background/60 absolute inset-0 grid place-items-center rounded-lg">
          <Spinner className="size-5" />
        </span>
      )}
      {a.status === "error" &&
        (onRetry ? (
          // The reason does not fit 64px legibly beside the action, so it is
          // the tooltip and what a screen reader hears after the label.
          <>
            <button
              type="button"
              onClick={() => onRetry(a.key)}
              aria-label={`Retry ${a.name}`}
              aria-describedby={errorId}
              title={error}
              className="text-destructive bg-background/50 hover:bg-background/70 focus-visible:ring-ring absolute inset-0 flex flex-col items-center justify-center gap-0.5 rounded-lg text-[10px] font-medium outline-none focus-visible:ring-2"
            >
              <RotateCwIcon className="size-4" />
              Retry
            </button>
            <span id={errorId} className="sr-only">
              {error}
            </span>
          </>
        ) : (
          <span
            title={error}
            className="text-destructive absolute inset-0 grid place-items-center rounded-lg px-1 text-center text-[10px] leading-tight"
          >
            {error}
          </span>
        ))}
    </>
  );
}

/** A staged non-image file: its tile, with the upload's progress or failure
    in place of its size until it is ready. */
function FileChip({
  attachment: a,
  onRetry,
}: {
  attachment: Attachment;
  onRetry?: (key: string) => void;
}) {
  const retry = a.status === "error" && !!onRetry;
  // Both routes report as they go, but only an artefact is staged at 0: a PDF
  // starts with no progress, so it reads "Uploading…" until its first report
  // lands and then shows the bar.
  const pct = a.progress === undefined ? undefined : Math.round(a.progress * 100);
  const detail =
    a.status === "uploading" ? (
      pct === undefined ? (
        "Uploading…"
      ) : (
        `Uploading ${pct}%`
      )
    ) : a.status === "error" ? (
      <span className="text-destructive" title={a.error}>
        {a.error ?? "Upload failed"}
      </span>
    ) : undefined;
  return (
    <div className="relative">
      <ArtefactTile
        compact
        name={a.name}
        mediaType={a.mediaType ?? "application/octet-stream"}
        size={a.size ?? 0}
        detail={detail}
        // Room on the right for the retry label laid over the tile.
        className={cn("pr-4", a.status === "error" && "border-destructive/50", retry && "pr-16")}
      />
      {retry && (
        // Over the whole tile rather than inside its detail line, which clips
        // what overflows it and would cut a 44px target down to a line of text.
        <button
          type="button"
          onClick={() => onRetry?.(a.key)}
          aria-label={`Retry ${a.name}`}
          title={a.error}
          className="text-destructive hover:bg-destructive/5 focus-visible:ring-ring absolute inset-0 flex items-center justify-end gap-1 rounded-xl pr-3 text-[11px] font-medium outline-none focus-visible:ring-2"
        >
          <RotateCwIcon className="size-3.5" />
          Retry
        </button>
      )}
      {a.status === "uploading" && pct !== undefined && (
        <span
          role="progressbar"
          aria-label={`Uploading ${a.name}`}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={pct}
          className="absolute inset-x-2 bottom-0.5 h-0.5 overflow-hidden rounded-full"
        >
          <span
            className="bg-primary block h-full transition-[width]"
            style={{ width: `${pct}%` }}
          />
        </span>
      )}
    </div>
  );
}
