import { Spinner } from "~/components/ui/spinner";

import { SendFailure } from "./SendFailure";

/**
 * What became of the last message, over the composer: held until its files
 * finish going up, or come back unsent. Said here so the composer is never
 * left looking as if nothing happened when Send was pressed.
 */
export function ComposerNotes({
  waiting,
  failure,
  canSend,
}: {
  waiting: boolean;
  failure?: { reason: string; onRetry?: () => void; onDismiss: () => void };
  /** Whether the composer would send now. A held message blocks a retry too,
      or the retry would overtake it. */
  canSend: boolean;
}) {
  return (
    <>
      {waiting && (
        <div role="status" className="text-muted-foreground flex items-center gap-2 px-4 pt-3 text-xs">
          {/* The note itself is the status; the spinner's own would say it twice. */}
          <Spinner role="presentation" aria-label={undefined} aria-hidden className="size-3.5 shrink-0" />
          <p className="min-w-0 flex-1 break-words">Sending once the uploads finish…</p>
        </div>
      )}
      {failure && (
        <SendFailure
          reason={failure.reason}
          canRetry={canSend && !waiting}
          onRetry={failure.onRetry}
          onDismiss={failure.onDismiss}
        />
      )}
    </>
  );
}
