import type { ComposerItem } from "~/protocol";

// The first-run-of-the-thread affordance: an empty transcript is exactly the
// moment the user needs a nudge, and the middle of it is where they are
// already looking. It is not a launcher — clicking only writes the token into
// the composer, so the next move (arguments, or just submit) stays theirs.
export function RecentSkills({
  items,
  seeded,
  onPick,
}: {
  items: ComposerItem[];
  /** True when nothing was remembered and this list is catalogue suggestions. */
  seeded: boolean;
  onPick: (item: ComposerItem) => void;
}) {
  if (items.length === 0) return null;
  return (
    <div className="fade-in mx-auto w-full max-w-sm">
      <p className="text-muted-foreground mb-2 text-center text-[11px] tracking-wide uppercase">
        {seeded ? "Try a skill" : "Recently used"}
      </p>
      <ul className="flex flex-col gap-1.5">
        {items.map((item) => (
          <li key={item.id}>
            <button
              type="button"
              onClick={() => onPick(item)}
              className="hover:bg-accent focus-visible:ring-ring bg-card/60 flex min-h-11 w-full items-center gap-2.5 rounded-lg border px-3 py-2 text-left outline-none focus-visible:ring-2 md:min-h-9"
            >
              <span className="shrink-0 font-mono text-[13px]">{item.insertText}</span>
              {item.description && (
                <span className="text-muted-foreground hidden min-w-0 flex-1 truncate text-[12px] sm:block">
                  {item.description}
                </span>
              )}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
