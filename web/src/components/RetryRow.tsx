import { cn } from "~/lib/utils";

/** A quiet inline action: a load, a retry, a way back after 4G drops a fetch. */
export const textButton =
  "text-muted-foreground hover:text-foreground focus-visible:ring-ring flex min-h-11 items-center gap-1.5 rounded-sm px-1 text-[12px] transition-colors outline-none focus-visible:ring-2 disabled:pointer-events-none md:min-h-0";

/** A failed fetch: what went wrong, and the way to try again. */
export function RetryRow({ message, onRetry, className }: { message: string; onRetry: () => void; className?: string }) {
  return (
    <div className={cn("flex flex-wrap items-center gap-x-2", className)}>
      <span role="alert" className="text-destructive text-[12px]">
        {message}
      </span>
      <button type="button" onClick={onRetry} className={textButton}>
        Retry
      </button>
    </div>
  );
}
