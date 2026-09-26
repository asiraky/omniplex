import type { ReactNode } from "react";

import { badgeLabel, formatBytes, typeFamily, type TypeFamily } from "~/lib/artefacts";
import { cn } from "~/lib/utils";

// Tints per family. Light text is darkened and dark text lifted so a 10px
// label keeps its contrast in both themes.
const FAMILY_TONE: Record<TypeFamily, string> = {
  html: "bg-orange-500/15 text-orange-700 dark:text-orange-300",
  doc: "bg-blue-500/15 text-blue-700 dark:text-blue-300",
  image: "bg-fuchsia-500/15 text-fuchsia-700 dark:text-fuchsia-300",
  media: "bg-violet-500/15 text-violet-700 dark:text-violet-300",
  data: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  code: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  other: "bg-muted text-muted-foreground",
};

/** The coloured square with the extension in it. Shared with the surface's
    header and fallback so a type looks the same everywhere it appears. */
export function TypeBadge({
  name,
  mediaType,
  className,
}: {
  name: string;
  mediaType: string;
  className?: string;
}) {
  const label = badgeLabel(name, mediaType);
  return (
    <span
      aria-hidden
      className={cn(
        "grid size-10 shrink-0 place-items-center rounded-lg font-mono text-[10px] font-semibold tracking-tight",
        label.length > 4 && "text-[8.5px]",
        FAMILY_TONE[typeFamily(name, mediaType)],
        className,
      )}
    >
      {label}
    </span>
  );
}

/**
 * An artefact as a card, the way a file appears in a chat: what kind of thing
 * it is at a glance, its name, and how big. The whole card is the button, so on
 * a phone the target is the card rather than a small icon inside it.
 *
 * `compact` is for inside a message bubble or the composer, where it sits in a
 * row with others and must not dominate the text.
 */
export function ArtefactTile({
  name,
  entry,
  mediaType,
  size,
  version,
  versions,
  source,
  compact = false,
  detail,
  onOpen,
  className,
}: {
  name: string;
  /** The file the artefact opens on, when its name has no extension to badge. */
  entry?: string;
  mediaType: string;
  size: number;
  /** The version this tile shows; labelled only when there is more than one. */
  version?: number;
  /** How many versions exist. */
  versions?: number;
  source?: "agent" | "upload";
  compact?: boolean;
  /** Replaces the size line: upload progress, an error. */
  detail?: ReactNode;
  /** Omitted, the tile is a picture of the file rather than a control. */
  onOpen?: () => void;
  className?: string;
}) {
  const shown = version ?? versions;
  const meta = [
    formatBytes(size),
    versions !== undefined && versions > 1 && shown !== undefined ? `v${shown}` : "",
    source === "upload" ? "Uploaded" : "",
  ]
    .filter(Boolean)
    .join(" · ");

  const body = (
    <>
      <TypeBadge name={entry || name} mediaType={mediaType} className={compact ? "size-8" : undefined} />
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span
          className={cn(
            "text-foreground font-medium break-words",
            compact ? "line-clamp-1 text-[12.5px] break-all" : "line-clamp-2 text-[13px] leading-snug",
          )}
          title={name}
        >
          {name}
        </span>
        <span className="text-muted-foreground truncate text-[11px]">{detail ?? meta}</span>
      </span>
    </>
  );

  const shape = cn(
    "bg-card flex items-center gap-2.5 rounded-xl border text-left",
    compact ? "w-full max-w-60 min-w-0 px-2 py-1.5" : "w-full p-2.5",
    className,
  );

  if (!onOpen) return <div className={shape}>{body}</div>;
  return (
    <button
      type="button"
      onClick={onOpen}
      className={cn(
        shape,
        "hover:bg-accent/50 focus-visible:ring-ring cursor-pointer transition-colors outline-none focus-visible:ring-2",
        // A thumb-sized target even when compact.
        compact && "min-h-11",
      )}
    >
      {body}
    </button>
  );
}
