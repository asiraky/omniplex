import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import { cn } from "~/lib/utils";

/**
 * What stands in for code that has not arrived: a spinner, or once the fetch
 * has failed, a way out. Chrome keeps a failed import for the life of the
 * page, so that way out is a reload.
 */
export function ChunkFallback({ failed, className }: { failed: boolean; className?: string }) {
  return (
    <div
      className={cn(
        "text-muted-foreground flex min-h-24 flex-col items-center justify-center gap-3 text-center",
        className,
      )}
    >
      {failed ? (
        <>
          <p>Couldn't load. Reloading the page tries again, but drops anything unsent.</p>
          <Button variant="outline" size="sm" onClick={() => location.reload()}>
            Reload
          </Button>
        </>
      ) : (
        <Spinner />
      )}
    </div>
  );
}
