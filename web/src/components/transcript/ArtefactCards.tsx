import { lazy, Suspense, useState } from "react";

import { ArtefactTile } from "~/components/artefacts/ArtefactTile";
import { drawsInline, type Artefact } from "~/lib/artefacts";
import type { Item } from "~/protocol";

import type { OpenArtefact } from "./Attachments";

// A page drawn inline loads its own code: most threads never show one, and
// those that do are waiting on the page itself anyway.
const InlineArtefact = lazy(() =>
  import("~/components/artefacts/InlineArtefact").then((m) => ({ default: m.InlineArtefact })),
);

// What the agent showed in a turn, one card per artefact. The card opens the
// file as it is now, since there is only ever the one.
// A turn that shows a pile of files shows the first few: nine full-width
// cards is a screen of scrolling on a phone before the next message.
const ARTEFACTS_SHOWN = 3;

export function ArtefactCards({
  threadId,
  items,
  artefacts,
  onOpen,
}: {
  threadId: string;
  items: Item[];
  artefacts: Artefact[];
  onOpen?: OpenArtefact;
}) {
  const [expanded, setExpanded] = useState(false);
  const rows = items.map((it) => {
    const a = artefacts.find((x) => x.id === it.artefactId);
    return { it, a, inline: drawsInline(it, a) };
  });
  // Only cards collapse: a page shown inline is the reply, not one of a pile.
  // Collapsing to hide a single card saves nothing.
  const cards = rows.filter((r) => !r.inline).length;
  const hidden = expanded || cards <= ARTEFACTS_SHOWN + 1 ? 0 : cards - ARTEFACTS_SHOWN;
  let cardsLeft = cards - hidden;
  const shown = rows.filter((r) => r.inline || cardsLeft-- > 0);

  return (
    <div className="fade-in flex flex-col gap-2">
      {shown.map(({ it, a, inline }) => {
        const open = onOpen && it.artefactId ? () => onOpen(it.artefactId!) : undefined;
        if (inline && a)
          return (
            // Held at about the size the frame first takes, header included.
            <Suspense key={it.id} fallback={<div className="bg-card h-[404px] w-full rounded-xl border" />}>
              <InlineArtefact threadId={threadId} artefact={a} onOpen={open} />
            </Suspense>
          );
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
            onOpen={open}
          />
        );
      })}
      {hidden > 0 && (
        <button
          type="button"
          onClick={() => setExpanded(true)}
          className="text-muted-foreground hover:text-foreground hover:bg-accent/50 focus-visible:ring-ring min-h-11 max-w-md rounded-xl border border-dashed px-3 text-left text-[12.5px] transition-colors outline-none focus-visible:ring-2 md:min-h-9"
        >
          Show {hidden} more {hidden === 1 ? "file" : "files"}
        </button>
      )}
    </div>
  );
}
