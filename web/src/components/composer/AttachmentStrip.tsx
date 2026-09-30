import { XIcon } from "lucide-react";

import { ArtefactTile } from "~/components/artefacts/ArtefactTile";
import { Spinner } from "~/components/ui/spinner";
import type { Attachment } from "~/lib/attachments";
import { cn } from "~/lib/utils";

/**
 * What is going out with the next message: pictures as thumbnails, anything
 * else as a file tile. Sized for a thumb: the remove button is always
 * visible, because there is no hover on a phone.
 */
export function AttachmentStrip({
  attachments,
  tooMuch,
  onRemove,
}: {
  attachments: Attachment[];
  tooMuch: boolean;
  onRemove?: (key: string) => void;
}) {
  if (attachments.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-2 px-3 pt-3">
      {attachments.map((a) => (
        <div key={a.key} className={cn("relative", a.kind === "file" && "max-w-60 min-w-0")}>
          {a.kind === "file" ? <FileChip attachment={a} /> : <ImageThumb attachment={a} />}
          <button
            type="button"
            onClick={() => onRemove?.(a.key)}
            aria-label={`Remove ${a.name}`}
            className="bg-background text-muted-foreground hover:text-foreground absolute -top-2 -right-2 grid size-6 place-items-center rounded-full border shadow-sm"
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

function ImageThumb({ attachment: a }: { attachment: Attachment }) {
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
      {a.status === "error" && (
        <span
          title={a.error}
          className="text-destructive absolute inset-0 grid place-items-center rounded-lg px-1 text-center text-[10px] leading-tight"
        >
          {a.error ?? "Upload failed"}
        </span>
      )}
    </>
  );
}

/** A staged non-image file: its tile, with the upload's progress or failure
    in place of its size until it is ready. */
function FileChip({ attachment: a }: { attachment: Attachment }) {
  // A PDF goes to the attachment store in one request with nothing to count,
  // so it has no progress to show; an artefact upload reports as it goes.
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
        className={cn("pr-4", a.status === "error" && "border-destructive/50")}
      />
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
