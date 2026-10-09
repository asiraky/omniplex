import { ChevronRightIcon } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "~/lib/utils";

/** One project's threads under a header that folds them away. */
export function ProjectGroup({
  name,
  count,
  newCount,
  folded,
  leaving,
  onToggle,
  children,
}: {
  name: string;
  count: number;
  /** Threads in the group that would show the `new` badge. */
  newCount: number;
  folded: boolean;
  /** The group's last thread is leaving, and taking the group with it. */
  leaving: boolean;
  onToggle: () => void;
  children: ReactNode;
}) {
  return (
    <div
      className={cn(
        "grid transition-[grid-template-rows,opacity] duration-[260ms] ease-out motion-reduce:transition-none",
        leaving ? "grid-rows-[0fr] opacity-0" : "mt-2 grid-rows-[1fr] first:mt-0",
      )}
    >
      <div className={cn("min-w-0", leaving && "overflow-hidden")}>
        {/* Sticky, so the project you are scrolling through keeps saying
           which one it is. Opaque rather than translucent: rows sliding
           under a blurred header read as a rendering fault on a phone. */}
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={!folded}
          aria-label={`${name}, ${count} thread${count === 1 ? "" : "s"}${newCount > 0 ? `, ${newCount} new` : ""}`}
          className="bg-sidebar text-muted-foreground hover:text-foreground focus-visible:ring-ring sticky top-0 z-10 flex w-full min-w-0 cursor-pointer items-center gap-1.5 rounded-md px-1.5 py-1.5 outline-none focus-visible:ring-2"
        >
          <ChevronRightIcon
            aria-hidden
            className={cn(
              "size-3.5 shrink-0 transition-transform duration-200 motion-reduce:transition-none",
              !folded && "rotate-90",
            )}
          />
          {/* The name as the user wrote it: it is a name, not a
             category, and a project called "pt-scratch" should not
             come back as "PT-SCRATCH". */}
          <span className="truncate text-[12px] font-semibold">{name}</span>
          {/* Folding a group must not fold away the one signal the list
             exists to carry: the header takes the badge over for its rows. */}
          {folded && newCount > 0 && (
            <span className="bg-primary text-primary-foreground dark:text-background ml-auto shrink-0 rounded-full px-1.5 text-[10px] leading-[15px] font-semibold">
              {newCount} new
            </span>
          )}
          <span
            className={cn(
              "shrink-0 pl-1.5 text-[11px] tabular-nums opacity-70",
              !(folded && newCount > 0) && "ml-auto",
            )}
          >
            {count}
          </span>
        </button>

        {/* Folded groups render nothing at all. A collapsed group that
           still costs a row of chrome is the thing #116 deleted; the
           header alone is the whole cost here. */}
        {!folded && <div className="mt-0.5">{children}</div>}
      </div>
    </div>
  );
}
