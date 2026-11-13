import { ChevronDownIcon, XIcon } from "lucide-react";
import {
  lazy,
  memo,
  Suspense,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ComponentProps,
  type ComponentType,
} from "react";

import { shallow, useThread, useThreadStore, type ThreadStore } from "~/app/threadStore";
import { IconButton } from "~/components/IconButton";
import { Spinner } from "~/components/ui/spinner";
import type { Artefact } from "~/lib/artefacts";
import type { CardSignIn } from "~/lib/cards";
import { chunk } from "~/lib/chunk";
import type { ThreadFindControls, ThreadMatch } from "~/lib/threadFind";
import { cn } from "~/lib/utils";
import type {
  ComposerItem,
  DiffTurn,
  PullRequest,
  QueuedPrompt,
  ScheduledPrompt,
  Turn,
  TurnStatus,
} from "~/protocol";
import { rowKey, rowTurnID } from "~/rows";
import { useAutoScroll } from "~/useAutoScroll";
import { useChunk, useIdlePrefetch } from "~/useChunk";
import { useLatest } from "~/useLatest";

import type { OpenArtefact } from "./transcript/Attachments";
import { EmptyTranscript } from "./transcript/EmptyTranscript";
import {
  clearHighlight,
  findItem,
  FindRevealProvider,
  flash,
  highlightIn,
  type Reveal,
} from "./transcript/findReveal";
import { MergedCard } from "./transcript/MergedCard";
import { QueuedPrompts } from "./transcript/QueuedPrompts";
import type { ScrollPosition } from "./transcript/scrollAnchor";
import { transcriptState, type TranscriptState } from "./transcript/transcriptState";
import { TranscriptRow } from "./transcript/TranscriptRow";
import { TurnStatusLine } from "./transcript/TurnStatusLine";
import { useOlderPages } from "./transcript/useOlderPages";
import { usePromptAnchor } from "./transcript/usePromptAnchor";
import { useScrollMemory } from "./transcript/useScrollMemory";
import { useTranscriptModel } from "./transcript/useTranscriptModel";
import { WorkingLine } from "./transcript/WorkingLine";
import { WorkspaceCard } from "./transcript/WorkspaceCard";

// Only a turn that died shows it, which most loads of a thread never see, so
// it loads on first use and stays out of the initial bundle. It is often
// fetched just after a reconnect; with no error boundary above it, a fetch
// that fails on a bad link would take the app down, so it says to reload.
type Card = ComponentType<
  ComponentProps<typeof import("./transcript/InterruptedCard").InterruptedCard>
>;
const CardUnavailable: Card = () => (
  <p className="text-muted-foreground text-[13px]">This turn stopped. Reload to see why.</p>
);
const InterruptedCard = lazy(() =>
  import("./transcript/InterruptedCard").then(
    (m): { default: Card } => ({ default: m.InterruptedCard }),
    () => ({ default: CardUnavailable }),
  ),
);

// The find bar opens on demand, so its code stays out of the initial bundle.
// It is fetched once the page settles, so opening it mounts the field at once
// (the phone keyboard only comes up for a field focused straight away).
const findBar = chunk(() => import("./transcript/FindBar").then((m) => m.ThreadFindBar));

function FindBarSlot(props: ComponentProps<typeof import("./transcript/FindBar").ThreadFindBar>) {
  useIdlePrefetch(findBar);
  const { loaded: Bar, failed } = useChunk(findBar, props.controls.open);
  if (!props.controls.open) return null;
  if (Bar) return <Bar {...props} />;
  if (!failed) return null;
  return (
    <div className="bg-background flex shrink-0 items-center gap-1 border-b px-2 py-1 md:px-3">
      <p className="text-destructive flex-1 text-[13px]">Find didn't load. Reload to try again.</p>
      <IconButton label="Close find" onClick={props.controls.close}>
        <XIcon />
      </IconButton>
    </div>
  );
}

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
const NO_RECENTS: ComposerItem[] = [];
const NO_QUEUED: QueuedPrompt[] = [];

type TranscriptProps = {
  /** Asks for the page above the window; called as the reader nears the top.
      Idempotent and fire-and-forget — the caller dedups in-flight asks. */
  onLoadOlder?: () => void;
  /** Where this thread was last scrolled — the position the parent kept from
      the previous time this thread was open, or the one a resumed page saved
      as it went to background (resume.ts). Applied once, on mount. */
  initialScroll?: ScrollPosition;
  /** Reports where the reader is, so the parent can hand it back the next time
      this thread is opened. Switching threads unmounts this component, so a
      position it kept to itself would die with it. */
  onScrollChange?: (threadId: string, position: ScrollPosition) => void;
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
  /** Cancels the continuation the server armed at a usage limit's reset. */
  onCancelContinuation?: (p: ScheduledPrompt) => Promise<void>;
  /** Opens the diff panel, on one turn's changes when a turn is given. */
  onOpenDiff: (path?: string, turn?: DiffTurn) => void;
  /** Opens the panel on the jobs surface, on one job when an id is given. */
  onOpenJobs?: (jobId?: string) => void;
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
  /** Rewords a queued prompt; resolves false when the server refused. */
  onEditQueued?: (queueId: string, prompt: string) => Promise<boolean> | boolean;
  /** Steers a queued prompt into the running turn, or starts a turn with it. */
  onSendQueued?: (queueId: string) => void;
  /** Lets the prompts the server is holding run. */
  onResumeQueue?: () => void;
  /** Takes the words of an edit whose prompt left the queue before it was
      saved, so they are not lost with the editor. */
  onQueuedEditLost?: (text: string) => void;
  /** The thread's harness can take a message into a running turn. */
  steers?: boolean;
  /** Opens the sign-in a saved card offers. */
  onCardSignIn?: (target: CardSignIn) => void;
  /** Names a model and effort, for the line where the thread switched. */
  describeSettings?: (model: string, effort: string) => string;
  /** Why the running turn has gone quiet (retrying, compacting), if it has. */
  turnStatus?: TurnStatus | null;
  /** Find in thread. Omitted or closed, there is no bar and no searching. */
  find?: ThreadFindControls;
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

/**
 * Find in thread's jumps, on the transcript's side. A match above the loaded
 * window is paged in first, in one request. The jump then sets `reveal`, which
 * opens whatever hides the item (a fold, a plan, a clamped prompt) in the same
 * render; once that has committed, the item is scrolled to the middle of the
 * view and its occurrences painted, or flashed where they cannot be.
 *
 * The paint is redone on every redraw while the jump stands, because a
 * re-rendered message has new text nodes and the old ranges are dead with
 * them. Text streaming in between redraws can still lose it until the next.
 */
function useFindJump(
  scrollerRef: { current: HTMLDivElement | null },
  state: TranscriptState,
  find: ThreadFindControls | undefined,
  release: () => void,
) {
  const [reveal, setReveal] = useState<Reveal | null>(null);
  const query = useRef("");
  const jumped = useRef<Reveal | null>(null);
  // Bumped by every jump and every clear, so a page that lands after either
  // has been superseded is not jumped to.
  const seq = useRef(0);
  const itemsBefore = useLatest(state.itemsBefore ?? 0);
  const loadTo = useLatest(find?.loadTo);

  // Resolves false when the match's page could not be loaded (a failed fetch,
  // or a page dropped as stale after a resync), so the bar can say so. A jump
  // superseded by a newer one or a clear is not a failure.
  const onJump = useCallback(
    async (match: ThreadMatch, q: string): Promise<boolean> => {
      const mine = ++seq.current;
      if (match.index < itemsBefore.current) {
        const loaded = await loadTo.current?.(match.index);
        if (mine !== seq.current) return true;
        if (!loaded) return false;
      }
      query.current = q;
      setReveal({ id: match.id });
      return true;
    },
    [itemsBefore, loadTo],
  );

  const onClear = useCallback(() => {
    seq.current++;
    jumped.current = null;
    clearHighlight();
    setReveal(null);
  }, []);

  useEffect(() => clearHighlight, []);

  // On the state as well as the reveal: a paged-in match may not have drawn
  // yet when the jump is asked for, and a redraw kills the painted ranges.
  useLayoutEffect(() => {
    const scroller = scrollerRef.current;
    if (!reveal || !scroller) return;
    const el = findItem(scroller, reveal.id);
    if (!el) return;
    const { ranges, painted } = highlightIn(el, query.current);
    if (jumped.current === reveal) return;
    jumped.current = reveal;
    // The pin would snap the view back to the tail on the next redraw.
    release();
    el.scrollIntoView({ block: "center" });
    // An item taller than the view can be centred with its match off screen.
    if (ranges[0]) centreIfHidden(scroller, ranges[0]);
    if (!painted) flash(el);
  }, [reveal, state, scrollerRef, release]);

  return { reveal, onJump, onClear };
}

// Counts the times the timeline lost turns: a rewind cutting it back. Live
// events and older pages only ever add turns, and the list is the whole
// thread's, not the window's.
function useTimelineCuts(turnCount: number): number {
  const [seen, setSeen] = useState(turnCount);
  const [cuts, setCuts] = useState(0);
  if (turnCount !== seen) {
    setSeen(turnCount);
    if (turnCount < seen) setCuts((c) => c + 1);
  }
  return cuts;
}

// Brings `range` to the middle of the scroller's view when it is outside it,
// allowing for the room the composer covers at the bottom.
function centreIfHidden(scroller: HTMLElement, range: Range) {
  const rect = range.getBoundingClientRect?.();
  if (!rect || (rect.width === 0 && rect.height === 0)) return;
  const box = scroller.getBoundingClientRect();
  const cover = parseFloat(getComputedStyle(scroller).scrollPaddingBottom) || 0;
  const bottom = box.bottom - cover;
  if (rect.top >= box.top && rect.bottom <= bottom) return;
  scroller.scrollTop += rect.top - box.top - (bottom - box.top - rect.height) / 2;
}

/**
 * The attached thread's conversation. It selects its own state from the thread
 * store, without the jobs and usage that tick between messages, and is
 * memoised: so it redraws when the conversation changes, and not for a job's
 * progress, a usage count, or the app rendering around it. That last needs
 * the parent to hand it the same props each time, which ThreadView does.
 */
export const Transcript = memo(function Transcript(props: TranscriptProps) {
  const store = useThreadStore();
  const state = useThread(store, transcriptState, shallow);
  // The store lets go of the thread the moment it is left, which reaches here
  // before the app unmounts the transcript; there is nothing to draw until it
  // does.
  if (!state) return null;
  return <TranscriptBody state={state} store={store} {...props} />;
});

function TranscriptBody({
  state,
  store,
  onLoadOlder,
  initialScroll,
  onScrollChange,
  onRetryProvision,
  onCleanup,
  onForceDelete,
  onOpenDiff,
  onOpenJobs,
  onOpenArtefact,
  pr,
  onFinish,
  recents = NO_RECENTS,
  recentsSeeded = false,
  onPickRecent,
  onDequeue,
  onEditQueued,
  onSendQueued,
  onResumeQueue,
  onQueuedEditLost,
  steers = false,
  onCardSignIn,
  describeSettings,
  turnStatus,
  find,
  ...interruptedProps
}: TranscriptProps & { state: TranscriptState; store: ThreadStore }) {
  // Follow the tail unless the reader has scrolled up; the button below is
  // how they get back. A restore that was scrolled up mounts unpinned, or the
  // first stick would snap it to the bottom over the restored position.
  const { scrollerRef, contentRef, pinned, stick, scrollToBottom, anchorTo, release } = useAutoScroll<
    HTMLDivElement,
    HTMLDivElement
  >(initialScroll?.atBottom ?? true);
  // Whenever anything it draws changes. The state is a fresh pick exactly
  // then, so it stands for all of it.
  useLayoutEffect(stick, [stick, state]);
  useScrollMemory(scrollerRef, state.threadId, store, initialScroll, onScrollChange);
  const hasOlder = (state.itemsBefore ?? 0) > 0;
  const sentinelRef = useOlderPages(scrollerRef, contentRef, state, hasOlder, onLoadOlder);
  const { reveal, onJump, onClear } = useFindJump(scrollerRef, state, find, release);
  const cuts = useTimelineCuts(state.turns.length);

  const {
    activity,
    empty,
    liveAgentId,
    lastPromptID,
    rows,
    interrupted,
    switchedTo,
    turnDiffs,
    turnNumbers,
    lastTurnID,
    recoveredTurns,
    continuation,
    heldFor,
  } = useTranscriptModel(state);
  usePromptAnchor(scrollerRef, state.threadId, lastPromptID, anchorTo);

  const artefacts = state.artefacts ?? NO_ARTEFACTS;
  const streamingId = state.phase === "turn" ? liveAgentId : undefined;

  return (
    <div className="relative flex min-h-0 flex-1 flex-col">
      {find && <FindBarSlot controls={find} cuts={cuts} onJump={onJump} onClear={onClear} />}
      <FindRevealProvider value={reveal}>
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

            {rows.map((row, i) => {
              // Each row is memoised, so it is handed only what it draws, worked
              // out here: a value it would have to look up across the thread is
              // a value that changes for every row whenever it does for one.
              // The changed-files card goes after the last row of the turn it
              // describes, the row whose successor belongs to a different turn.
              const turnID = rowTurnID(row);
              const nextTurnID = i + 1 < rows.length ? rowTurnID(rows[i + 1]) : undefined;
              const diff = turnID && turnID !== nextTurnID ? turnDiffs.get(turnID) : undefined;
              const item = row.kind === "item" ? row.item : undefined;
              // Only a prompt shows attachments, so only a prompt or an
              // artefacts row redraws when a new artefact is shown. The jobs are
              // not handed down at all: a spawn card selects its own from the
              // store, as they tick far more often than anything else.
              const showsArtefacts =
                row.kind === "artefacts" || (item?.kind === "message" && item.role === "user");
              return (
                <TranscriptRow
                  key={rowKey(row)}
                  row={row}
                  diff={diff}
                  turnNumber={diff && turnID ? (turnNumbers.get(turnID) ?? 0) : 0}
                  latest={diff !== undefined && turnID === lastTurnID}
                  onOpenDiff={onOpenDiff}
                  threadId={state.threadId}
                  streaming={item !== undefined && item.id === streamingId}
                  artefacts={showsArtefacts ? artefacts : NO_ARTEFACTS}
                  onOpenArtefact={onOpenArtefact}
                  onOpenJobs={onOpenJobs}
                  recovered={item?.turnId ? recoveredTurns.get(item.turnId) : undefined}
                  onCardSignIn={onCardSignIn}
                  describeSettings={describeSettings}
                  previewed={item !== undefined && item === activity?.thought}
                />
              );
            })}

            {activity && <WorkingLine activity={activity} />}
            {/* Shown whatever else the tail is doing: a retry or a compaction is
                exactly when a live tool line or half-streamed text looks hung. */}
            <TurnStatusLine status={turnStatus} running={state.phase === "turn"} />

            {interrupted && (
              <Suspense fallback={null}>
                <InterruptedCard
                  turn={interrupted}
                  switchedTo={switchedTo}
                  continuation={continuation}
                  {...interruptedProps}
                />
              </Suspense>
            )}

            <QueuedPrompts
              queued={state.queuedPrompts ?? NO_QUEUED}
              threadId={state.threadId}
              artefacts={artefacts}
              heldFor={heldFor}
              busy={state.phase === "turn"}
              steers={steers}
              onDequeue={onDequeue}
              onEdit={onEditQueued}
              onSend={onSendQueued}
              onResume={onResumeQueue}
              onEditLost={onQueuedEditLost}
            />

            {/* Last, because it is the latest news about the work above it. */}
            {pr?.merged && <MergedCard pr={pr} onFinish={onFinish} />}
          </div>
        </div>
      </FindRevealProvider>

      {!pinned && <ScrollToBottom onClick={scrollToBottom} />}
    </div>
  );
}
