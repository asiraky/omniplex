import { Fragment } from "react";

import { ChangedFiles } from "~/components/ChangedFiles";
import type { Artefact } from "~/lib/artefacts";
import type { CardSignIn } from "~/lib/cards";
import type { Job, TurnDiff } from "~/protocol";
import { rowTurnID, type Row } from "~/rows";

import { ArtefactCards } from "./ArtefactCards";
import type { OpenArtefact } from "./Attachments";
import { CardRow } from "./CardRow";
import { JobsCard } from "./JobsCard";
import { Message } from "./Message";
import { NoticeCard } from "./NoticeCard";
import { ToolCard, ToolRun, TurnFold } from "./ToolCards";

type RowContext = {
  threadId: string;
  /** The agent message still streaming in, if one is. */
  streamingId: string | undefined;
  artefacts: Artefact[];
  onOpenArtefact?: OpenArtefact;
  jobs: Job[];
  onOpenJobs?: () => void;
  recoveredTurns: Map<string, "restart" | "continue">;
  /** Opens the sign-in a saved card offers. */
  onCardSignIn?: (target: CardSignIn) => void;
};

function RowBody({
  row,
  threadId,
  streamingId,
  artefacts,
  onOpenArtefact,
  jobs,
  onOpenJobs,
  recoveredTurns,
  onCardSignIn,
}: RowContext & { row: Row }) {
  if (row.kind === "fold") return <TurnFold turn={row.turn} items={row.items} />;
  if (row.kind === "run") return <ToolRun items={row.items} live={row.live} />;
  if (row.kind === "jobs") return <JobsCard items={row.items} jobs={jobs} onOpen={onOpenJobs} />;
  if (row.kind === "artefacts")
    return <ArtefactCards items={row.items} artefacts={artefacts} onOpen={onOpenArtefact} />;
  if (row.item.kind === "tool") return <ToolCard item={row.item} />;
  if (row.item.kind === "notice") return <NoticeCard item={row.item} />;
  if (row.item.kind === "card") return <CardRow item={row.item} onSignIn={onCardSignIn} />;
  return (
    <Message
      item={row.item}
      threadId={threadId}
      streaming={row.item.id === streamingId}
      artefacts={artefacts}
      onOpenArtefact={onOpenArtefact}
      recovered={(row.item.turnId && recoveredTurns.get(row.item.turnId)) || undefined}
    />
  );
}

/** One row of the transcript, and the changed-files card for its turn if it closes one. */
export function TranscriptRow({
  row,
  nextRow,
  turnDiffs,
  lastTurnID,
  onOpenDiff,
  ...context
}: RowContext & {
  row: Row;
  nextRow: Row | undefined;
  turnDiffs: Map<string, TurnDiff>;
  lastTurnID: string | undefined;
  onOpenDiff: (path?: string) => void;
}) {
  // The card goes after the last row of the turn it describes, which is
  // the row whose successor belongs to a different turn.
  const turnID = rowTurnID(row);
  const nextTurnID = nextRow ? rowTurnID(nextRow) : undefined;
  const diff = turnID && turnID !== nextTurnID ? turnDiffs.get(turnID) : undefined;

  return (
    // A plain fragment, not a `content-visibility: auto` box. Skipping
    // the render of off-screen rows costs nothing to measure and a lot
    // to scroll: a row that has never been on screen is laid out at
    // its `contain-intrinsic-size` guess, and rows here are anything
    // from a one-line tool call to a screenful of markdown, so every
    // one the reader scrolls up into swaps a 120px placeholder for its
    // real height and shoves the view. That is the scroll-up stutter,
    // and it healed only on the way back down because by then each row
    // had been rendered once and its size remembered.
    <Fragment>
      <RowBody row={row} {...context} />
      {diff && <ChangedFiles diff={diff} latest={turnID === lastTurnID} onOpenDiff={onOpenDiff} />}
    </Fragment>
  );
}
