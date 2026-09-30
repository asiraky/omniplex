import { CoffeeIcon, MessagesSquareIcon, PlusIcon } from "lucide-react";

import { Button } from "./ui/button";
import { Spinner } from "./ui/spinner";

/**
 * What the content column shows with nothing attached.
 *
 * There are three of these and they are genuinely different situations, so
 * they say different things. A single oversized "New thread" button was
 * answering all three with a call to action nobody asked for: on a phone it
 * was the whole landing screen, and on a desktop with threads in the list it
 * was pointing away from them.
 */
export function EmptyState({
  restoring,
  attaching,
  hasThreads,
  onNew,
}: {
  restoring: boolean;
  attaching: boolean;
  hasThreads: boolean;
  onNew: () => void;
}) {
  // Mid-restore. Saying anything here would only be contradicted a moment
  // later, so it says nothing and just holds the space.
  if (restoring) {
    return (
      <div className="flex flex-1 items-center justify-center" aria-busy="true">
        <span className="sr-only">Reopening your last thread…</span>
        <Spinner className="text-muted-foreground/60 size-5" />
      </div>
    );
  }

  // Selecting clears the old snapshot before attaching to the new thread.
  // That gap is loading, not an invitation to create another thread.
  if (attaching) {
    return (
      <div
        className="flex flex-1 flex-col items-center justify-center gap-3 pb-16"
        aria-busy="true"
      >
        <Spinner className="text-muted-foreground/60 size-6" />
        <p className="text-muted-foreground text-[13px]">Attaching to thread…</p>
      </div>
    );
  }

  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-4 px-6 pb-16 text-center">
      {hasThreads ? (
        <>
          <MessagesSquareIcon aria-hidden className="text-muted-foreground/40 size-7" />
          <div className="max-w-xs">
            <p className="text-[15px] font-medium">Nothing open</p>
            <p className="text-muted-foreground mt-1.5 text-[13px] leading-relaxed">
              Pick a thread from the list to jump back into it.
            </p>
          </div>
        </>
      ) : (
        <>
          <CoffeeIcon aria-hidden className="text-muted-foreground/40 size-7" />
          <div className="max-w-xs">
            <p className="text-[15px] font-medium">All caught up</p>
            <p className="text-muted-foreground mt-1.5 text-[13px] leading-relaxed">
              Nothing is running. Put your feet up — or start something new.
            </p>
          </div>
        </>
      )}
      {/* Offered, not insisted on, but still a real target for a thumb. */}
      <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={onNew}>
        <PlusIcon />
        New thread
      </Button>
    </div>
  );
}
