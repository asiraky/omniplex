import { Spinner } from "~/components/ui/spinner";

/**
 * A message has been sent but is held in the browser until its files finish
 * going up. Said where a failed send is said, so the composer is never left
 * looking as if nothing happened when Send was pressed.
 */
export function UploadWait() {
  return (
    <div role="status" className="text-muted-foreground flex items-center gap-2 px-4 pt-3 text-xs">
      {/* The note itself is the status; the spinner's own would say it twice. */}
      <Spinner role="presentation" aria-label={undefined} aria-hidden className="size-3.5 shrink-0" />
      <p className="min-w-0 flex-1 break-words">Sending once the uploads finish…</p>
    </div>
  );
}
