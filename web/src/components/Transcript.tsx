import { ChevronDownIcon } from "lucide-react";
import { useLayoutEffect } from "react";

import { IconButton } from "~/components/IconButton";
import { Spinner } from "~/components/ui/spinner";
import type { Artefact } from "~/lib/artefacts";
import type { CardSignIn } from "~/lib/cards";
import { cn } from "~/lib/utils";
import type { ComposerItem, Job, PullRequest, ThreadState, Turn } from "~/protocol";
import { useAutoScroll } from "~/useAutoScroll";

import type { OpenArtefact } from "./transcript/Attachments";
import { EmptyTranscript } from "./transcript/EmptyTranscript";
import { InterruptedCard } from "./transcript/InterruptedCard";
import { MergedCard } from "./transcript/MergedCard";
import { QueuedCard } from "./transcript/QueuedCard";
import { TranscriptRow } from "./transcript/TranscriptRow";
import { useOlderPages } from "./transcript/useOlderPages";
import { usePromptAnchor } from "./transcript/usePromptAnchor";
import { useScrollMemory } from "./transcript/useScrollMemory";
import { useTranscriptModel } from "./transcript/useTranscriptModel";
import { WorkspaceCard } from "./transcript/WorkspaceCard";

// Room reserved beneath the transcript's tail: the overlay's measured height
// (`--composer-h`, published by App from a ResizeObserver) plus a headroom band
// so the last message rests clear of the composer with air above it rather than
// pinned to its top edge. The 9rem fallback matches the collapsed composer for
// the first frame, before the measurement lands.
const TAIL_RESERVE = "calc(var(--composer-h, 9rem) + 6rem)";
// The tail's room plus whatever extra the scroll hook is asking for to lift a
// just-sent prompt clear of the composer (`--anchor-reserve`, 0 when it is not).
const CONTENT_RESERVE = `calc(${TAIL_RESERVE} + var(--anchor-reserve, 0px))`;
// An empty thread has no tail to rest, so it keeps clear of the composer and
// no more: the headroom on top of a fixed-height empty state was what pushed
// a thread with nothing in it a few pixels past the screen.
const EMPTY_RESERVE = "var(--composer-h, 9rem)";

const NO_ARTEFACTS: Artefact[] = [];
const NO_JOBS: Job[] = [];
const NO_RECENTS: ComposerItem[] = [];

type TranscriptProps = {
  state: ThreadState;
  /** True when the server holds items older than the loaded window. */
  hasOlder?: boolean;
  /** Asks for the page above the window; called as the reader nears the top.
      Idempotent and fire-and-forget — the caller dedups in-flight asks. */
  onLoadOlder?: () => void;
  /** Where this thread was last scrolled — the position the parent kept from
      the previous time this thread was open, or the one a resumed page saved
      as it went to background (resume.ts). Applied once, on mount. */
  initialScroll?: { top: number; atBottom: boolean };
  /** Reports where the reader is, so the parent can hand it back the next time
      this thread is opened. Switching threads unmounts this component, so a
      position it kept to itself would die with it. */
  onScrollChange?: (threadId: string, top: number, atBottom: boolean) => void;
  onRetryProvision: () => void;
  onCleanup: () => void;
  onForceDelete: () => void;
  onContinue: () => void;
  /** Opens this thread provider's interactive sign-in flow, when it has one. */
  onLogin?: () => void;
  /** The provider instance named in authentication failures. */
  providerName?: string;
  /** True while that instance reports ready; gates the auth card's Retry. */
  providerReady?: boolean;
  /** Re-sends a failed turn's prompt, on explicit request only. */
  onRetryTurn?: (turn: Turn) => void;
  /** Other accounts of this thread's harness, offered on a usage limit. */
  switchTargets?: { id: string; name: string }[];
  /** Moves the thread to another account and re-sends the given turn;
      resolves false when the switch was refused or failed. */
  onSwitchAccount?: (instance: string, turn: Turn) => Promise<boolean>;
  onOpenDiff: (path?: string) => void;
  /** The thread's jobs, for the spawn cards to read live status from. */
  jobs?: Job[];
  /** Opens the panel on the jobs surface. */
  onOpenJobs?: () => void;
  /** Opens an artefact in the panel. */
  onOpenArtefact?: OpenArtefact;
  /** The thread branch's pull request, when omniplex could find one. */
  pr?: PullRequest | null;
  /** Opens the delete confirmation for this thread. */
  onFinish: () => void;
  /** Skills to offer on an empty transcript, already filtered against this
      thread's live catalogue by the parent — never offer what it cannot run. */
  recents?: ComposerItem[];
  /** True when `recents` are catalogue suggestions rather than real history. */
  recentsSeeded?: boolean;
  /** Writes the skill's token into the composer. Omitted, the list is hidden. */
  onPickRecent?: (item: ComposerItem) => void;
  /** Takes a queued prompt back before it runs. */
  onDequeue?: (queueId: string) => void;
  /** Opens the sign-in a saved card offers. */
  onCardSignIn?: (target: CardSignIn) => void;
};

// Anchored to the scroller rather than to the content, so it sits in
// the same place wherever the transcript happens to be scrolled. It
// rides just above the composer, tracking its measured height so the two
// never overlap however tall the composer grows. The wrapper is inert to
// the pointer: only the button itself may take a click, or a strip of
// dead space would run across the transcript.
//
// It clears the 2rem fade band that sits on top of the composer and
// stacks above it, so the gradient washes over scrolling content but
// never across the button's face.
function ScrollToBottom({ onClick }: { onClick: () => void }) {
  return (
    <div
      className="pointer-events-none absolute inset-x-0 z-20 flex justify-center"
      style={{ bottom: "calc(var(--composer-h, 9rem) + 2rem)" }}
    >
      <IconButton
        label="Scroll to bottom"
        variant="outline"
        onClick={onClick}
        className="fade-in bg-background pointer-events-auto rounded-full shadow-md"
      >
        <ChevronDownIcon />
      </IconButton>
    </div>
  );
}

export function Transcript({
  state,
  hasOlder = false,
  onLoadOlder,
  initialScroll,
  onScrollChange,
  onRetryProvision,
  onCleanup,
  onForceDelete,
  onOpenDiff,
  jobs = NO_JOBS,
  onOpenJobs,
  onOpenArtefact,
  pr,
  onFinish,
  recents = NO_RECENTS,
  recentsSeeded = false,
  onPickRecent,
  onDequeue,
  onCardSignIn,
  ...interruptedProps
}: TranscriptProps) {
  // Follow the tail unless the reader has scrolled up; the button below is
  // how they get back. A restore that was scrolled up mounts unpinned, or the
  // first stick would snap it to the bottom over the restored position.
  const { scrollerRef, contentRef, pinned, stick, scrollToBottom, anchorTo } = useAutoScroll<
    HTMLDivElement,
    HTMLDivElement
  >(initialScroll?.atBottom ?? true);
  useLayoutEffect(stick, [stick, state.items, state.seq]);
  useScrollMemory(scrollerRef, state, initialScroll, onScrollChange);
  const sentinelRef = useOlderPages(scrollerRef, contentRef, state, hasOlder, onLoadOlder);

  const {
    empty,
    liveAgentId,
    lastPromptID,
    rows,
    interrupted,
    switchedTo,
    turnDiffs,
    lastTurnID,
    recoveredTurns,
  } = useTranscriptModel(state);
  usePromptAnchor(scrollerRef, state.threadId, lastPromptID, anchorTo);

  const artefacts = state.artefacts ?? NO_ARTEFACTS;

  return (
    <div className="relative flex min-h-0 flex-1 flex-col">
      <div
        ref={scrollerRef}
        data-transcript-scroller
        className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain"
        // So a programmatic scrollIntoView lands the target above the floating
        // composer rather than behind it, matching the padding below.
        // overflow-anchor is off because prepending older pages is corrected
        // by hand in useOlderPages — Chrome's native anchoring would correct it
        // too and the view would jump a page down, while Safari has no native
        // anchoring at all. One mechanism, ours, on every browser.
        style={{ scrollPaddingBottom: TAIL_RESERVE, overflowAnchor: "none" }}
      >
        {/* The floating composer overlays the tail, so the content reserves
            real room below it — its measured height plus headroom — and grows
            that room as the composer does, so the tail can always scroll clear
            and rests with breathing space rather than jammed against the input. */}
        <div
          ref={contentRef}
          className={cn("mx-auto flex max-w-3xl flex-col gap-3.5 px-4 pt-6 md:px-5", empty && "min-h-full")}
          style={{ paddingBottom: empty ? EMPTY_RESERVE : CONTENT_RESERVE }}
        >
          <WorkspaceCard
            state={state}
            onRetry={onRetryProvision}
            onCleanup={onCleanup}
            onForceDelete={onForceDelete}
          />
          {/* The top of the loaded window, not of the conversation: scrolling
              up to here fetches the page above (the observer's margin means
              the fetch usually starts before this row is even seen). */}
          {hasOlder && (
            <div
              ref={sentinelRef}
              className="text-muted-foreground flex items-center justify-center gap-2 py-3 text-[13px]"
            >
              <Spinner className="size-3.5" /> Loading earlier…
            </div>
          )}
          {/* The empty state used to hide behind any workspace phase at all,
              which left a dismissed-but-ready workspace showing nothing
              whatever. It only needs to stand aside while the provisioner is
              still working or is asking for a decision — once the workspace is
              ready, an empty transcript is an empty transcript. */}
          {empty && (
            <EmptyTranscript
              recents={recents}
              recentsSeeded={recentsSeeded}
              onPickRecent={onPickRecent}
            />
          )}

          {rows.map((row, i) => (
            <TranscriptRow
              key={row.kind === "item" ? row.item.id : row.id}
              row={row}
              nextRow={rows[i + 1]}
              turnDiffs={turnDiffs}
              lastTurnID={lastTurnID}
              onOpenDiff={onOpenDiff}
              threadId={state.threadId}
              streamingId={state.phase === "turn" ? liveAgentId : undefined}
              artefacts={artefacts}
              onOpenArtefact={onOpenArtefact}
              jobs={jobs}
              onOpenJobs={onOpenJobs}
              recoveredTurns={recoveredTurns}
              onCardSignIn={onCardSignIn}
            />
          ))}

          {state.phase === "turn" &&
            liveAgentId === undefined &&
            !rows.some((r) => r.kind === "run" && r.live) && (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <Spinner className="text-primary size-3.5" /> thinking…
              </div>
            )}

          {interrupted && (
            <InterruptedCard turn={interrupted} switchedTo={switchedTo} {...interruptedProps} />
          )}

          {(state.queuedPrompts ?? []).map((q) => (
            <QueuedCard
              key={q.queueId}
              queued={q}
              threadId={state.threadId}
              artefacts={artefacts}
              onDequeue={(id) => onDequeue?.(id)} />
          ))}

          {/* Last, because it is the latest news about the work above it. */}
          {pr?.merged && <MergedCard pr={pr} onFinish={onFinish} />}
        </div>
      </div>

      {!pinned && <ScrollToBottom onClick={scrollToBottom} />}
    </div>
  );
}
