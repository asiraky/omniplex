import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from "react";

import { useDraft } from "~/app/draftStore";
import { useStash } from "~/app/stashStore";
import { useFailedSend, useWaitingSend } from "~/app/outbox";
import { shallow, useThread, useThreadStore } from "~/app/threadStore";
import type { ComposerDrafts } from "~/app/useComposerDrafts";
import { useOverlayHeight } from "~/app/useOverlayHeight";
import type { PanelControls } from "~/app/usePanel";
import type { RecentSkills } from "~/app/useRecentSkills";
import type { ScheduleEditor } from "~/app/useScheduleEditor";
import type { ThreadCommands } from "~/app/useThreadCommands";
import type { ThreadHarness } from "~/app/useThreadHarness";
import type { DraftPreset } from "~/app/useThreadSelection";
import type { ConnectionStatus } from "~/client";
import type { ScrollPosition } from "~/components/transcript/scrollAnchor";
import type { CardSignIn } from "~/lib/cards";
import { elicitationDrafts } from "~/lib/elicitationDraft";
import { describeSettings, pickerInstances, resolveInstance } from "~/lib/models";
import { promptHistory, sameHistory } from "~/lib/promptHistory";
import { OpenPathContext } from "~/lib/openPath";
import { ReadAloudContext } from "~/lib/readAloud";
import { quoteInto, ReplyContext } from "~/lib/reply";
import { threadTitle } from "~/lib/threadTitle";
import { toast } from "~/lib/toast";
import { useLatest } from "~/useLatest";
import type {
  HarnessMeta,
  PendingElicitation,
  PullRequest,
  ScheduledPrompt,
  ThreadMeta,
  ThreadState,
  Turn,
  TurnStatus,
} from "~/protocol";

import type { AuthWires } from "./AuthFlowDialog";
import { Composer } from "./Composer";
import { ConnectionStrip } from "./ConnectionStrip";
import { useComposerItems } from "./composer/useComposerItems";
import { JobsStrip } from "./JobsStrip";
import { PermissionPrompt } from "./PermissionPrompt";
import { ScheduledPrompts } from "./ScheduledPrompts";
import { SuggestionChip } from "./SuggestionChip";
import { Transcript } from "./Transcript";

// Both open on something the user or the agent does mid-thread, never on the
// first paint, so they load on first use and stay out of the initial bundle.
const ElicitationPrompt = lazy(() =>
  import("./ElicitationPrompt").then((m) => ({ default: m.ElicitationPrompt })),
);
// Cards and their sign-ins come from an agent asking to change the setup,
// which most threads never do.
const PendingCard = lazy(() => import("./cards/PendingCard"));
const FlowDialog = lazy(() =>
  import("./AuthFlowDialog").then((m) => ({ default: m.FlowDialog })),
);
const ScheduleDialog = lazy(() =>
  import("./ScheduleDialog").then((m) => ({ default: m.ScheduleDialog })),
);

/**
 * The attached thread: its transcript, and floating over the tail of it the
 * composer with anything that blocks the turn stacked above.
 *
 * It takes no state: each part below selects the slice of the thread it draws
 * from the store, so a frame rerenders the parts it changed, and this only
 * for a new title.
 */
export function ThreadView({
  activeId,
  meta,
  harnesses,
  scroll,
  commands,
  harness,
  panel,
  pr,
  recents,
  schedule,
  store,
  authWires,
  onLogin,
  onForceDelete,
  onFinish,
  readAloud,
  status,
  turnStatus,
  onSendAndNewThread,
}: {
  activeId: string | null;
  meta: ThreadMeta | undefined;
  harnesses: HarnessMeta[];
  scroll: {
    positionOf: (id: string) => ScrollPosition | undefined;
    record: (id: string, position: ScrollPosition) => void;
  };
  commands: ThreadCommands;
  harness: ThreadHarness;
  panel: PanelControls;
  pr: PullRequest | null;
  recents: RecentSkills;
  schedule: ScheduleEditor;
  store: ComposerDrafts;
  authWires: AuthWires;
  onLogin: (instanceId: string) => void;
  onForceDelete: (id: string) => void;
  onFinish: (meta: ThreadMeta) => void;
  /** An ElevenLabs key is stored, so messages get a play button and the
      composer a mic. */
  readAloud: boolean;
  status: ConnectionStatus;
  /** Why the running turn has gone quiet, while the harness says so. */
  turnStatus: TurnStatus | null;
  /** A message just went: open a new draft on this thread's project and agent. */
  onSendAndNewThread?: (preset: DraftPreset) => void;
}) {
  const { layoutRef, overlayRef } = useOverlayHeight(activeId);
  const stateTitle = useThread(useThreadStore(), titleOf);
  const harnessId = useThread(useThreadStore(), harnessOf) ?? "";
  const title = threadTitle(meta, stateTitle);
  const speech = useMemo(
    () => (readAloud && activeId ? { threadId: activeId, title } : null),
    [readAloud, activeId, title],
  );
  const provider = harness.activeProviderInstance;
  const instanceId = meta?.providerInstance ?? "";
  const describe = useMemo(() => {
    const instance = resolveInstance(pickerInstances(harnesses), instanceId, harnessId);
    if (!instance) return undefined;
    return (model: string, effort: string) => describeSettings(instance, model, effort);
  }, [harnesses, instanceId, harnessId]);
  // A reply's "reply" quotes it into this thread's draft and takes the cursor
  // there, since what comes next is typing the answer. While the composer is
  // locked (thread closed, workspace going away or broken), replies offer nothing.
  const writable = useThread(useThreadStore(), composerWritable);
  const { getDraft, setDraft } = store;
  const { composerRef } = recents;
  const reply = useCallback(
    (quote: string) => {
      if (!activeId) return;
      const next = quoteInto(getDraft(activeId), quote);
      setDraft(activeId, next);
      composerRef.current?.focusEnd(next.length);
    },
    [activeId, composerRef, getDraft, setDraft],
  );
  // A sign-in a saved card offers from its row in the transcript.
  const [signIn, setSignIn] = useState<CardSignIn | null>(null);
  const steers = harnessSteers(harnesses, harnessId);

  // The transcript is memoised and selects its own state, so it redraws when
  // that changes and not whenever the app does. That holds only while what it
  // is handed here stays the same object across renders: every handler is
  // kept to the identities it is built from.
  const { threadCommand } = commands;
  const { switchAccount } = harness;
  const onContinue = useCallback(() => threadCommand("continue_thread"), [threadCommand]);
  const onRetryProvision = useCallback(() => threadCommand("retry_provision"), [threadCommand]);
  const onCleanup = useCallback(() => threadCommand("cleanup_thread"), [threadCommand]);
  const loginId = provider?.canLogin ? provider.id : undefined;
  const onProviderLogin = useMemo(
    () => (loginId ? () => onLogin(loginId) : undefined),
    [loginId, onLogin],
  );
  const onSwitchAccount = useCallback(
    (instance: string, retry: Turn) => switchAccount(instance, { retry }),
    [switchAccount],
  );
  const { runAction } = schedule;
  const onCancelContinuation = useCallback(
    (p: ScheduledPrompt) => runAction(activeId, "cancel_schedule", p),
    [runAction, activeId],
  );
  const onForceDeleteThread = useCallback(
    () => activeId && onForceDelete(activeId),
    [activeId, onForceDelete],
  );
  // The list entry is read at the click: it changes with every broadcast that
  // touches this thread, and the transcript has no use for those.
  const metaRef = useLatest(meta);
  const onFinishThread = useCallback(() => {
    if (metaRef.current) onFinish(metaRef.current);
  }, [metaRef, onFinish]);
  // Where this thread was scrolled when it was opened, which is all the
  // transcript reads of it, on mount. Asked again only for a new thread (the
  // transcript is keyed on it), so a scroll recorded since does not hand the
  // memoised transcript a new object on every render.
  const { positionOf } = scroll;
  const initialScroll = useMemo(
    () => (activeId ? positionOf(activeId) : undefined),
    [activeId, positionOf],
  );

  return (
    <div ref={layoutRef} className="relative flex min-h-0 flex-1 flex-col">
      {/* Content scrolling up dissolves into the header rather than
          being cut by a border. */}
      <div className="from-background to-background/0 pointer-events-none absolute top-0 right-(--scrollbar-w,0px) left-0 z-10 h-8 bg-gradient-to-b" />
      <ConnectionStrip status={status} />

      <OpenPathContext.Provider value={panel.openPath}>
        <ReadAloudContext.Provider value={speech}>
          <ReplyContext.Provider value={writable ? reply : null}>
            <Transcript
              key={activeId}
              onLoadOlder={commands.loadOlderItems}
              initialScroll={initialScroll}
              onScrollChange={scroll.record}
              onContinue={onContinue}
              onLogin={onProviderLogin}
              providerName={provider?.displayName}
              providerReady={provider?.availability.state === "ready"}
              onRetryTurn={harness.retryTurn}
              switchTargets={harness.switchTargets}
              onSwitchAccount={onSwitchAccount}
              onCancelContinuation={onCancelContinuation}
              onRetryProvision={onRetryProvision}
              onCleanup={onCleanup}
              onForceDelete={onForceDeleteThread}
              onOpenDiff={panel.openDiff}
              onOpenJobs={panel.openJobs}
              onOpenArtefact={panel.openArtefact}
              pr={pr}
              onFinish={onFinishThread}
              recents={recents.items}
              recentsSeeded={recents.seeded}
              onPickRecent={recents.pick}
              onDequeue={commands.dequeue}
              onEditQueued={commands.editQueued}
              onSendQueued={commands.sendQueued}
              onResumeQueue={commands.resumeQueue}
              onQueuedEditLost={commands.keepLostEdit}
              steers={steers}
              onCardSignIn={setSignIn}
              describeSettings={describe}
              turnStatus={turnStatus}
            />
          </ReplyContext.Provider>
        </ReadAloudContext.Provider>
      </OpenPathContext.Provider>

      {signIn && (
        <Suspense fallback={null}>
          <FlowDialog
            key={signIn.key}
            wires={authWires}
            title={signIn.title}
            description={signIn.description}
            begin={signIn.begin}
            onFinished={() => {
              if (signIn.reconnect) commands.reconnectMcp(signIn.reconnect.name, signIn.reconnect.project);
              setSignIn(null);
              toast.success("Signed in");
            }}
            onClose={() => setSignIn(null)}
          />
        </Suspense>
      )}

      {/* The mirror of the header fade: content dissolves into the
          composer instead of sliding under a hard edge. It sits just
          above the overlay, tracking its measured height. */}
      <div
        className="from-background to-background/0 pointer-events-none absolute right-(--scrollbar-w,0px) left-0 z-10 h-8 bg-gradient-to-t"
        style={{ bottom: "var(--composer-h, 9rem)" }}
      />

      {/* The input floats over the transcript's tail instead of sitting
          in a full-width tray. Anything that blocks the turn (a
          permission or elicitation) stacks above it. It is opaque: the
          fade above ends in solid background, and a see-through overlay
          let text show again at full strength right under that edge. */}
      <div
        ref={overlayRef}
        className="bg-background absolute right-(--scrollbar-w,0px) bottom-0 left-0 z-10"
      >
        <ComposerDock
          activeId={activeId}
          meta={meta}
          harnesses={harnesses}
          commands={commands}
          harness={harness}
          panel={panel}
          recents={recents}
          schedule={schedule}
          store={store}
          dictate={readAloud}
          onSendAndNewThread={onSendAndNewThread}
        />
      </div>
    </div>
  );
}

// Whether the thread's harness can take a message into a running turn, which
// is what a queued prompt's "Steer now" and the composer's steer both need.
function harnessSteers(harnesses: HarnessMeta[], harnessId: string) {
  return harnesses.find((h) => h.id === harnessId)?.steers ?? false;
}

function hasCard(e: PendingElicitation): e is PendingElicitation & { card: NonNullable<PendingElicitation["card"]> } {
  return Boolean(e.card);
}

// Preparing is not closed: the worktree is still being cut, but the user can
// already write the first message; only sending waits. Cleaning is different:
// the workspace is going away, so there is nothing left to write to.
function workspaceStatus(phase: ThreadState["phase"]) {
  const preparing = phase === "creating" || phase === "provisioning";
  const cleaning = phase === "cleaning";
  const failed = phase === "provision_failed" || phase === "cleanup_failed";
  const placeholder = cleaning
    ? "Cleaning up workspace…"
    : preparing
      ? "Preparing workspace…"
      : failed
        ? "Workspace needs attention"
        : undefined;
  return { busy: preparing || cleaning, cleaning, failed, placeholder };
}

/** The composer, and what stacks above it: prompts, running jobs, schedules. */
function ComposerDock({
  activeId,
  meta,
  harnesses,
  commands,
  harness,
  panel,
  recents,
  schedule,
  store,
  dictate,
  onSendAndNewThread,
}: {
  activeId: string | null;
  meta: ThreadMeta | undefined;
  harnesses: HarnessMeta[];
  commands: ThreadCommands;
  harness: ThreadHarness;
  panel: PanelControls;
  recents: RecentSkills;
  schedule: ScheduleEditor;
  store: ComposerDrafts;
  dictate: boolean;
  onSendAndNewThread?: (preset: DraftPreset) => void;
}) {
  const state = useThread(useThreadStore(), dockFields, shallow);
  // Paired with the state's own thread id, not activeId: for a render after a
  // switch the two can disagree, and the wrong pairing would drop the drafts
  // of the thread being opened.
  const threadId = state?.threadId;
  const pendingElicitations = state?.pendingElicitations;
  useEffect(() => {
    if (threadId) {
      elicitationDrafts.keepOnly(threadId, (pendingElicitations ?? []).map((e) => e.requestId));
    }
  }, [threadId, pendingElicitations]);
  if (!state) return null;
  const pending = state.pendingPermissions?.[0];
  const elicitations = state.pendingElicitations ?? [];
  // A card is a proposal, not a question: it does not hold the turn, so it
  // shows beside a plain question rather than queueing behind it.
  const cards = elicitations.filter(hasCard);
  const card = cards[0];
  const elicitation = elicitations.find((e) => !e.card);
  const workspace = workspaceStatus(state.phase);
  const editing = schedule.editing;
  return (
    <>
      {card && (
        <Suspense fallback={null}>
          <PendingCard
            key={card.requestId}
            request={card}
            position={cards.length > 1 ? `1 of ${cards.length}` : undefined}
            // Folded to its one line when something that does hold the turn
            // needs the room.
            defaultOpen={!pending && !elicitation}
            resolve={(action, edits) => commands.resolveCard(card.requestId, action, edits)}
          />
        </Suspense>
      )}

      {pending && (
        <PermissionPrompt
          request={pending}
          onResolve={(outcome, optionId) =>
            commands.resolvePermission(pending.requestId, outcome, optionId)
          }
        />
      )}

      {elicitation && (
        <Suspense fallback={null}>
          <ElicitationPrompt
            // A fresh prompt per question, so one queued behind another does
            // not open on the first one's answers.
            key={elicitation.requestId}
            threadId={state.threadId}
            request={elicitation}
            onResolve={(action, value) =>
              commands.resolveElicitation(elicitation.requestId, action, value)
            }
          />
        </Suspense>
      )}

      <JobsStrip onOpen={() => panel.openJobs()} />

      <ScheduledPrompts
        schedules={state.scheduledPrompts ?? []}
        disabled={state.closed || workspace.busy || workspace.failed}
        onEdit={(p) => activeId && schedule.openExisting(activeId, p)}
        onAction={(action, p) => schedule.runAction(activeId, action, p)}
      />
      {editing && (
        <Suspense fallback={null}>
          <ScheduleDialog
            key={`schedule:${editing.id}`}
            initialText={editing.text}
            imageCount={editing.imageIds.length}
            schedule={editing.schedule}
            onClose={schedule.close}
            onSave={schedule.save}
          />
        </Suspense>
      )}
      {/* Directly above the composer, one line: the keyboard opening
          moves the whole overlay, never this alone. Tapping the text hands
          it to the composer the way a reply's quote does. */}
      <SuggestionChip
        threadId={activeId}
        draftStore={store.draftStore}
        outbox={store.outbox}
        staged={!!activeId && (store.attachments[activeId]?.length ?? 0) > 0}
        onEdit={(text) => {
          if (!activeId) return;
          store.setDraft(activeId, text);
          recents.composerRef.current?.focusEnd(text.length);
        }}
        onSend={(text) => recents.composerRef.current?.send(text)}
      />
      <ThreadComposer
        key={activeId}
        activeId={activeId}
        meta={meta}
        harnesses={harnesses}
        commands={commands}
        harness={harness}
        recents={recents}
        schedule={schedule}
        store={store}
        workspace={workspace}
        dictate={dictate}
        onSendAndNewThread={onSendAndNewThread}
      />
    </>
  );
}

/**
 * The attached thread's composer, keyed per thread by the dock so its command
 * catalogue starts empty on a switch rather than showing the last thread's.
 */
function ThreadComposer({
  activeId,
  meta,
  harnesses,
  commands,
  harness,
  recents,
  schedule,
  store,
  workspace,
  dictate,
  onSendAndNewThread,
}: {
  activeId: string | null;
  meta: ThreadMeta | undefined;
  harnesses: HarnessMeta[];
  commands: ThreadCommands;
  harness: ThreadHarness;
  recents: RecentSkills;
  schedule: ScheduleEditor;
  store: ComposerDrafts;
  workspace: ReturnType<typeof workspaceStatus>;
  dictate: boolean;
  onSendAndNewThread?: (preset: DraftPreset) => void;
}) {
  const catalogue = useComposerItems(commands.loadComposerItems);
  // Subscribed here rather than read from the app, so a keystroke rerenders
  // this composer and not the thread view, transcript and sidebar around it.
  const draft = useDraft(store.draftStore, activeId);
  const failed = useFailedSend(store.outbox, activeId);
  const waiting = useWaitingSend(store.outbox, activeId);
  // The one part of the thread view that draws usage (the context meter), so
  // the one that rerenders on a usage tick.
  const state = useThread(useThreadStore(), composerFields, shallow);
  // The thread's prompts as strings, so a streamed reply, which changes the
  // items but not what was asked, does not rerender the composer.
  const history = useThread(useThreadStore(), historyOf, sameHistory);
  const stashed = useStash(store.stashStore, activeId);
  const { stashDraft, unstash, dropStash } = store;
  const stash = useMemo(
    () =>
      activeId
        ? {
            entries: stashed,
            save: (textOnly?: boolean) => stashDraft(activeId, textOnly),
            take: (id: string) => unstash(activeId, id),
            drop: (id: string) => dropStash(activeId, id),
          }
        : undefined,
    [activeId, stashed, stashDraft, unstash, dropStash],
  );
  if (!state) return null;
  return (
    <Composer
      ref={recents.composerRef}
      draft={draft}
      onDraftChange={(text) => activeId && store.setDraft(activeId, text)}
      disabled={!composerWritable(state)}
      disabledPlaceholder={workspace.placeholder}
      busy={state.phase === "turn"}
      steers={harnessSteers(harnesses, state.harness)}
      onSend={commands.send}
      // The next thread takes this one's project and agent, as it is running
      // now rather than as it started.
      onSendAndNewThread={
        onSendAndNewThread &&
        (() =>
          onSendAndNewThread({
            projectId: meta?.projectId,
            // Absent is the whole project, which the draft takes as "".
            folderId: meta?.folderId ?? "",
            agent: {
              harness: state.harness,
              instance: meta?.providerInstance ?? "",
              model: state.model,
              effort: state.effort,
              serviceTier: state.serviceTier,
            },
          }))
      }
      onSchedule={() => activeId && schedule.openFromComposer(activeId)}
      onCancel={commands.cancel}
      attachments={activeId ? (store.attachments[activeId] ?? []) : []}
      onAttachFiles={store.attachFiles}
      onRemoveAttachment={(key) => activeId && store.removeAttachment(activeId, key)}
      onRetryAttachment={(key) => activeId && store.retryUpload(activeId, key)}
      waiting={waiting}
      harnesses={harnesses}
      harness={state.harness}
      instance={meta?.providerInstance ?? ""}
      model={state.model}
      effort={state.effort}
      serviceTier={state.serviceTier}
      onSwitchModel={harness.switchModel}
      onSwitchEffort={harness.switchEffort}
      onSwitchServiceTier={harness.switchServiceTier}
      onSwitchAccount={(instance, model) =>
        void harness.switchAccount(instance, { model, confirm: true })
      }
      usage={state.usage}
      catalogue={catalogue}
      onRunClientAction={commands.runClientComposerAction}
      onRunComposerAction={commands.runComposerAction}
      onCommandUsed={recents.noteUsed}
      dictation={dictate && activeId ? { thread: activeId } : undefined}
      history={history}
      stash={stash}
      failure={
        failed && {
          reason: failed.reason,
          // A message whose file did not upload never went: retrying it as it
          // stands would send it without the file. The file's own retry, on
          // its chip, is the way back.
          onRetry: failed.cannotRetry ? undefined : () => commands.retry(failed),
          onDismiss: () => store.outbox.dismiss(failed.commandId),
        }
      }
    />
  );
}

const titleOf = (state: ThreadState | null) => state?.title;
const NO_HISTORY: string[] = [];
const historyOf = (state: ThreadState | null) => (state ? promptHistory(state.items, state.turns) : NO_HISTORY);
const composerWritable = (state: Pick<ThreadState, "closed" | "phase"> | null) => {
  if (!state || state.closed) return false;
  const workspace = workspaceStatus(state.phase);
  return !workspace.cleaning && !workspace.failed;
};
const harnessOf = (state: ThreadState | null) => state?.harness;

// What the dock draws. Not jobs: the strip selects those itself, so a job's
// progress rerenders the strip and not the prompts and composer beside it.
// Null with no thread: the store lets go of the state the moment a thread is
// left, and the subscribers hear of it before the app has unmounted them.
function dockFields(s: ThreadState | null) {
  return (
    s && {
      threadId: s.threadId,
      pendingPermissions: s.pendingPermissions,
      pendingElicitations: s.pendingElicitations,
      scheduledPrompts: s.scheduledPrompts,
      phase: s.phase,
      closed: s.closed,
    }
  );
}

function composerFields(s: ThreadState | null) {
  return (
    s && {
      closed: s.closed,
      phase: s.phase,
      harness: s.harness,
      model: s.model,
      effort: s.effort,
      serviceTier: s.serviceTier,
      usage: s.usage,
    }
  );
}
