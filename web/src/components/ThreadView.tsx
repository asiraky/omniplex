import { lazy, Suspense, useState } from "react";

import type { ComposerDrafts } from "~/app/useComposerDrafts";
import { useOverlayHeight } from "~/app/useOverlayHeight";
import type { PanelControls } from "~/app/usePanel";
import type { RecentSkills } from "~/app/useRecentSkills";
import type { ScheduleEditor } from "~/app/useScheduleEditor";
import type { ScrollPosition } from "~/app/useScrollMemory";
import type { ThreadCommands } from "~/app/useThreadCommands";
import type { ThreadHarness } from "~/app/useThreadHarness";
import type { CardSignIn } from "~/lib/cards";
import { liveJobCount } from "~/lib/jobs";
import { OpenPathContext } from "~/lib/openPath";
import { toast } from "~/lib/toast";
import type { HarnessMeta, PendingElicitation, PullRequest, ThreadMeta, ThreadState } from "~/protocol";

import type { AuthWires } from "./AuthFlowDialog";
import { Composer } from "./Composer";
import { useComposerItems } from "./composer/useComposerItems";
import { JobsStrip } from "./JobsStrip";
import { PermissionPrompt } from "./PermissionPrompt";
import { ScheduledPrompts } from "./ScheduledPrompts";
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
 */
export function ThreadView({
  state,
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
}: {
  state: ThreadState;
  activeId: string | null;
  meta: ThreadMeta | undefined;
  harnesses: HarnessMeta[];
  scroll: {
    positionOf: (id: string) => ScrollPosition | undefined;
    record: (id: string, top: number, atBottom: boolean) => void;
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
}) {
  const { layoutRef, overlayRef } = useOverlayHeight(activeId);
  const provider = harness.activeProviderInstance;
  // A sign-in a saved card offers from its row in the transcript.
  const [signIn, setSignIn] = useState<CardSignIn | null>(null);

  return (
    <div ref={layoutRef} className="relative flex min-h-0 flex-1 flex-col">
      {/* Content scrolling up dissolves into the header rather than
          being cut by a border. */}
      <div className="from-background to-background/0 pointer-events-none absolute top-0 right-(--scrollbar-w,0px) left-0 z-10 h-8 bg-gradient-to-b" />

      <OpenPathContext.Provider value={panel.openPath}>
        <Transcript
          key={activeId}
          state={state}
          hasOlder={(state.itemsBefore ?? 0) > 0}
          onLoadOlder={commands.loadOlderItems}
          initialScroll={activeId ? scroll.positionOf(activeId) : undefined}
          onScrollChange={scroll.record}
          onContinue={() => commands.threadCommand("continue_thread")}
          onLogin={provider?.canLogin ? () => onLogin(provider.id) : undefined}
          providerName={provider?.displayName}
          providerReady={provider?.availability.state === "ready"}
          onRetryTurn={harness.retryTurn}
          switchTargets={harness.switchTargets}
          onSwitchAccount={(instance, retry) => harness.switchAccount(instance, { retry })}
          onRetryProvision={() => commands.threadCommand("retry_provision")}
          onCleanup={() => commands.threadCommand("cleanup_thread")}
          onForceDelete={() => activeId && onForceDelete(activeId)}
          onOpenDiff={panel.openDiff}
          jobs={state.jobs}
          onOpenJobs={panel.openJobs}
          onOpenArtefact={panel.openArtefact}
          pr={pr}
          onFinish={() => meta && onFinish(meta)}
          recents={recents.items}
          recentsSeeded={recents.seeded}
          onPickRecent={recents.pick}
          onDequeue={commands.dequeue}
          onCardSignIn={setSignIn}
        />
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
          state={state}
          activeId={activeId}
          meta={meta}
          harnesses={harnesses}
          commands={commands}
          harness={harness}
          panel={panel}
          recents={recents}
          schedule={schedule}
          store={store}
        />
      </div>
    </div>
  );
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
  state,
  activeId,
  meta,
  harnesses,
  commands,
  harness,
  panel,
  recents,
  schedule,
  store,
}: {
  state: ThreadState;
  activeId: string | null;
  meta: ThreadMeta | undefined;
  harnesses: HarnessMeta[];
  commands: ThreadCommands;
  harness: ThreadHarness;
  panel: PanelControls;
  recents: RecentSkills;
  schedule: ScheduleEditor;
  store: ComposerDrafts;
}) {
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
            request={elicitation}
            onResolve={(action, value) =>
              commands.resolveElicitation(elicitation.requestId, action, value)
            }
          />
        </Suspense>
      )}

      {liveJobCount(state.jobs) > 0 && <JobsStrip jobs={state.jobs} onOpen={panel.openJobs} />}

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
      <ThreadComposer
        key={activeId}
        state={state}
        activeId={activeId}
        meta={meta}
        harnesses={harnesses}
        commands={commands}
        harness={harness}
        recents={recents}
        schedule={schedule}
        store={store}
        workspace={workspace}
      />
    </>
  );
}

/**
 * The attached thread's composer, keyed per thread by the dock so its command
 * catalogue starts empty on a switch rather than showing the last thread's.
 */
function ThreadComposer({
  state,
  activeId,
  meta,
  harnesses,
  commands,
  harness,
  recents,
  schedule,
  store,
  workspace,
}: {
  state: ThreadState;
  activeId: string | null;
  meta: ThreadMeta | undefined;
  harnesses: HarnessMeta[];
  commands: ThreadCommands;
  harness: ThreadHarness;
  recents: RecentSkills;
  schedule: ScheduleEditor;
  store: ComposerDrafts;
  workspace: ReturnType<typeof workspaceStatus>;
}) {
  const catalogue = useComposerItems(commands.loadComposerItems);
  return (
    <Composer
      ref={recents.composerRef}
      draft={activeId ? (store.drafts[activeId] ?? "") : ""}
      onDraftChange={(text) => activeId && store.setDraft(activeId, text)}
      disabled={state.closed || workspace.cleaning || workspace.failed}
      disabledPlaceholder={workspace.placeholder}
      busy={state.phase === "turn"}
      onSend={commands.send}
      onSchedule={() => activeId && schedule.openFromComposer(activeId)}
      onCancel={commands.cancel}
      attachments={activeId ? (store.attachments[activeId] ?? []) : []}
      onAttachFiles={store.attachFiles}
      onRemoveAttachment={(key) => activeId && store.removeAttachment(activeId, key)}
      harnesses={harnesses}
      harness={state.harness}
      instance={meta?.providerInstance ?? ""}
      model={state.model}
      effort={state.effort}
      onSwitchModel={harness.switchModel}
      onSwitchEffort={harness.switchEffort}
      onSwitchAccount={(instance, model) =>
        void harness.switchAccount(instance, { model, confirm: true })
      }
      usage={state.usage}
      catalogue={catalogue}
      onRunClientAction={commands.runClientComposerAction}
      onRunComposerAction={commands.runComposerAction}
      onCommandUsed={recents.noteUsed}
    />
  );
}
