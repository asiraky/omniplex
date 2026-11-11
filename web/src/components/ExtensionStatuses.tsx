import { cn } from "~/lib/utils";
import type { ExtensionStatus } from "~/protocol";

/**
 * The status lines harness extensions keep for the thread, one quiet chip per
 * key. Text only: the key is the extension's own identifier, so it goes in the
 * tooltip rather than on screen. Renders nothing when no extension has said
 * anything, so the header keeps its usual height.
 *
 * Inline, beside the title on a desktop, the chips squeeze and truncate before
 * the title gives up its room. As a line of their own under a phone's title
 * they keep their width and the line scrolls sideways instead.
 */
export function ExtensionStatuses({
  statuses,
  inline,
}: {
  statuses: ExtensionStatus[] | undefined;
  inline: boolean;
}) {
  if (!statuses?.length) return null;
  return (
    <ul
      aria-label="Extension status"
      className={cn(
        "flex min-w-0 items-center gap-1",
        inline ? "max-w-[40%] overflow-hidden" : "scroll-shadow-x overflow-x-auto [scrollbar-width:none]",
      )}
    >
      {statuses.map((s) => (
        <li
          key={s.key}
          title={s.key}
          className={cn(
            "text-muted-foreground max-w-56 truncate rounded-full border px-2 py-0.5 text-[11px]",
            inline ? "min-w-0" : "shrink-0",
          )}
        >
          {s.text}
        </li>
      ))}
    </ul>
  );
}
