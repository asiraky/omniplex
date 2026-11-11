import { memo } from "react";

import { ChangedFiles } from "~/components/ChangedFiles";
import type { Artefact } from "~/lib/artefacts";
import type { CardSignIn } from "~/lib/cards";
import type { Job, TurnDiff } from "~/protocol";
import { rowKey, type Row } from "~/rows";

import { ArtefactCards } from "./ArtefactCards";
import type { OpenArtefact } from "./Attachments";
import { CardRow } from "./CardRow";
import { JobsCard } from "./JobsCard";
import { Message, type Recovered } from "./Message";
import { NoticeCard } from "./NoticeCard";
import { PlanRow } from "./PlanRow";
import { ToolCard, ToolRun, TurnFold } from "./ToolCards";

// Everything a row gets is already resolved for that row, never a lookup over
// the whole thread: a row is memoised, and a prop shared by every row (the
// streaming id, the next row, a map of every turn) would redraw them all
// whenever any one of them changed.
type RowContext = {
  threadId: string;
  /** This row is the agent message still streaming in. */
  streaming: boolean;
  artefacts: Artefact[];
  onOpenArtefact?: OpenArtefact;
  jobs: Job[];
  onOpenJobs?: () => void;
  /** Why this row's prompt was sent by the server, if it was. */
  recovered: Recovered | undefined;
  /** Opens the sign-in a saved card offers. */
  onCardSignIn?: (target: CardSignIn) => void;
  /** Names a model and effort, for the line where the thread switched. */
  describeSettings?: (model: string, effort: string) => string;
};

function RowBody({
  row,
  threadId,
  streaming,
  artefacts,
  onOpenArtefact,
  jobs,
  onOpenJobs,
  recovered,
  onCardSignIn,
  describeSettings,
}: RowContext & { row: Row }) {
  if (row.kind === "fold")
    return <TurnFold threadId={threadId} turn={row.turn} items={row.items} continued={row.continued} />;
  if (row.kind === "run") return <ToolRun threadId={threadId} items={row.items} live={row.live} />;
  if (row.kind === "jobs") return <JobsCard items={row.items} jobs={jobs} onOpen={onOpenJobs} />;
  if (row.kind === "artefacts")
    return <ArtefactCards items={row.items} artefacts={artefacts} onOpen={onOpenArtefact} />;
  if (row.item.kind === "tool") return <ToolCard threadId={threadId} item={row.item} />;
  if (row.item.kind === "notice") return <NoticeCard item={row.item} describeSettings={describeSettings} />;
  if (row.item.kind === "card") return <CardRow item={row.item} onSignIn={onCardSignIn} />;
  if (row.item.kind === "plan") return <PlanRow item={row.item} />;
  return (
    <Message
      item={row.item}
      threadId={threadId}
      streaming={streaming}
      artefacts={artefacts}
      onOpenArtefact={onOpenArtefact}
      recovered={recovered}
    />
  );
}

/** One row of the transcript, and the changed-files card for its turn if it closes one. */
export const TranscriptRow = memo(function TranscriptRow({
  row,
  diff,
  latest,
  onOpenDiff,
  ...context
}: RowContext & {
  row: Row;
  /** What this row's turn changed, when this is the turn's last row. */
  diff: TurnDiff | undefined;
  /** The diff is the newest turn's. */
  latest: boolean;
  onOpenDiff: (path?: string) => void;
}) {
  return (
    // A `display: contents` box, not a `content-visibility: auto` one.
    // Skipping the render of off-screen rows costs nothing to measure and
    // a lot to scroll: a row that has never been on screen is laid out at
    // its `contain-intrinsic-size` guess, and rows here are anything from
    // a one-line tool call to a screenful of markdown, so every one the
    // reader scrolls up into swaps a 120px placeholder for its real height
    // and shoves the view. That is the scroll-up stutter, and it healed
    // only on the way back down because by then each row had been rendered
    // once and its size remembered.
    //
    // The box is here only to carry the row's id, which is how the scroll
    // memory finds the row the reader was on (scrollAnchor.ts). `contents`
    // keeps it out of layout, so the transcript's gap still falls between
    // the row and its changed-files card as if they were loose children.
    <div data-row-id={rowKey(row)} className="contents">
      <RowBody row={row} {...context} />
      {diff && <ChangedFiles diff={diff} latest={latest} onOpenDiff={onOpenDiff} />}
    </div>
  );
});
