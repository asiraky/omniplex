import { CircleAlertIcon, XIcon } from "lucide-react";

/**
 * Why the last message did not go, in the composer it came back to. Retry
 * sends that message again as it went, under the same command id, so a send
 * that did reach the server after all is not run twice.
 *
 * Both buttons get a 44px hit area on a touch screen: this note turns up most
 * on a phone on a bad connection. The areas reach over the top of the
 * textarea below, so they sit above it.
 */
export function SendFailure({
  reason,
  canRetry,
  onRetry,
  onDismiss,
}: {
  reason: string;
  canRetry: boolean;
  onRetry: () => void;
  onDismiss: () => void;
}) {
  return (
    <div role="alert" className="text-destructive flex items-start gap-2 px-4 pt-3 text-xs">
      <CircleAlertIcon className="mt-px size-3.5 shrink-0" />
      <p className="min-w-0 flex-1 break-words">
        Not sent: {reason.replace(/[.\s]+$/, "")}.{" "}
        <button
          type="button"
          onClick={onRetry}
          disabled={!canRetry}
          className="hit-44 relative z-10 font-medium underline underline-offset-2 disabled:no-underline disabled:opacity-60"
        >
          Retry
        </button>
      </p>
      <button
        type="button"
        onClick={onDismiss}
        aria-label="Dismiss"
        className="text-muted-foreground hover:text-foreground hit-44 relative z-10 -my-1 grid size-6 shrink-0 place-items-center rounded"
      >
        <XIcon className="size-3.5" />
      </button>
    </div>
  );
}
