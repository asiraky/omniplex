import { PackageIcon } from "lucide-react";
import { useMemo, useState } from "react";

import { ArtefactTile } from "~/components/artefacts/ArtefactTile";
import type { Artefact } from "~/lib/artefacts";
import { cn } from "~/lib/utils";

export type ArtefactFilter = "all" | "agent" | "upload";

const FILTERS: { id: ArtefactFilter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "agent", label: "Agent" },
  { id: "upload", label: "Uploads" },
];

/** Most recently shown first, narrowed to one source. An upload the agent
    has since revised and shown is the agent's work now. */
export function listArtefacts(artefacts: Artefact[], filter: ArtefactFilter): Artefact[] {
  return artefacts
    .filter((a) => filter === "all" || a.source === filter)
    .sort((a, b) => b.shownAt - a.shownAt);
}

/**
 * The "Artefacts" panel surface: everything the session has produced or been
 * handed, newest first. A grid when the panel is wide, one column when it is
 * not — decided by the panel's width, not the screen's, since a docked panel on
 * a desktop can be as narrow as a phone.
 */
export function ArtefactList({
  artefacts,
  onOpen,
}: {
  artefacts: Artefact[];
  onOpen: (id: string) => void;
}) {
  const [filter, setFilter] = useState<ArtefactFilter>("all");
  const shown = useMemo(() => listArtefacts(artefacts, filter), [artefacts, filter]);

  if (artefacts.length === 0) {
    return (
      <div className="text-muted-foreground flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
        <PackageIcon className="size-8 opacity-60" />
        <p className="text-foreground text-[13px] font-medium">No artefacts yet</p>
        <p className="max-w-72 text-[12px] leading-relaxed">
          When the agent makes something for you (a report, a page, a chart) it shows it with its show_file tool,
          and it lands here. You can also drop any file into the composer to hand it to the agent.
        </p>
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div role="radiogroup" aria-label="Show" className="flex items-center gap-1.5 border-b px-2 py-1.5">
        {FILTERS.map((f) => (
          <button
            key={f.id}
            type="button"
            role="radio"
            aria-checked={filter === f.id}
            onClick={() => setFilter(f.id)}
            className={cn(
              "focus-visible:ring-ring min-h-9 rounded-full border px-3 text-[12px] transition-colors outline-none focus-visible:ring-2 md:min-h-7",
              filter === f.id
                ? "bg-primary text-primary-foreground border-transparent"
                : "text-muted-foreground hover:text-foreground hover:bg-accent/50",
            )}
          >
            {f.label}
          </button>
        ))}
      </div>
      <div className="@container scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain p-2">
        {shown.length === 0 ? (
          <p className="text-muted-foreground px-2 py-10 text-center text-[12px]">
            {filter === "upload" ? "Nothing uploaded yet." : "The agent has not shown you anything yet."}
          </p>
        ) : (
          <ul className="grid grid-cols-1 gap-2 @lg:grid-cols-2 @4xl:grid-cols-3">
            {shown.map((a) => (
              <li key={a.id} className="min-w-0">
                <ArtefactTile
                  name={a.name}
                  entry={a.entry}
                  mediaType={a.mediaType}
                  size={a.size}
                  files={a.files}
                  source={a.source}
                  onOpen={() => onOpen(a.id)}
                />
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
