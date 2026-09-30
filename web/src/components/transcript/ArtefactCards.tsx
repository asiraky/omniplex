import { useState } from "react";

import { ArtefactTile } from "~/components/artefacts/ArtefactTile";
import type { Artefact } from "~/lib/artefacts";
import type { Item } from "~/protocol";

import type { OpenArtefact } from "./Attachments";

// What the agent showed in a turn, one card per artefact. The card opens the
// file as it is now, since there is only ever the one.
// A turn that shows a pile of files shows the first few: nine full-width
// cards is a screen of scrolling on a phone before the next message.
const ARTEFACTS_SHOWN = 3;

export function ArtefactCards({
  items,
  artefacts,
  onOpen,
}: {
  items: Item[];
  artefacts: Artefact[];
  onOpen?: OpenArtefact;
}) {
  const [expanded, setExpanded] = useState(false);
  // Collapsing to hide a single card saves nothing.
  const shown =
    expanded || items.length <= ARTEFACTS_SHOWN + 1 ? items : items.slice(0, ARTEFACTS_SHOWN);
  return (
    <div className="fade-in flex flex-col gap-2">
      {shown.map((it) => {
        const a = artefacts.find((x) => x.id === it.artefactId);
        return (
          <ArtefactTile
            key={it.id}
            name={a?.name ?? it.title ?? "artefact"}
            entry={a?.entry}
            mediaType={a?.mediaType ?? it.mediaType ?? ""}
            size={a?.size ?? it.size ?? 0}
            files={a?.files}
            source="agent"
            detail={a?.note}
            className="max-w-md"
            onOpen={onOpen && it.artefactId ? () => onOpen(it.artefactId!) : undefined}
          />
        );
      })}
      {shown.length < items.length && (
        <button
          type="button"
          onClick={() => setExpanded(true)}
          className="text-muted-foreground hover:text-foreground hover:bg-accent/50 focus-visible:ring-ring min-h-11 max-w-md rounded-xl border border-dashed px-3 text-left text-[12.5px] transition-colors outline-none focus-visible:ring-2 md:min-h-9"
        >
          Show {items.length - shown.length} more{" "}
          {items.length - shown.length === 1 ? "file" : "files"}
        </button>
      )}
    </div>
  );
}
