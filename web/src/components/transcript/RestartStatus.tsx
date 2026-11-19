import { RotateCwIcon } from "lucide-react";

import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";

/**
 * A restart the thread owes: waiting on the running turn to end, with the way
 * to take it back, or under way now, which can take seconds while the harness
 * stops and starts. One muted line at the tail, under the working indicator,
 * so on a phone it shows where the reader is already looking. Once it has
 * happened the timeline's note says so, and this is gone.
 */
export function RestartStatus({
  pending,
  restarting,
  onCancel,
}: {
  pending: boolean;
  restarting: boolean;
  onCancel?: () => void;
}) {
  if (!pending && !restarting) return null;
  return (
    <div
      role="status"
      aria-live="polite"
      className="text-muted-foreground fade-in flex min-w-0 items-center gap-2 text-[12px]"
    >
      {pending ? (
        <RotateCwIcon className="size-3.5 shrink-0" aria-hidden />
      ) : (
        <Spinner className="size-3.5 shrink-0" aria-hidden />
      )}
      <span className="min-w-0 flex-1 truncate">
        {pending ? "Restarting when this turn ends" : "Restarting the thread…"}
      </span>
      {pending && onCancel && (
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-9 shrink-0 text-[12px] pointer-coarse:min-h-11 md:h-7"
          aria-label="Cancel restart"
          onClick={onCancel}
        >
          Cancel
        </Button>
      )}
    </div>
  );
}
