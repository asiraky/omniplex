import { useCallback, useMemo } from "react";

import { NEW_THREAD } from "./app/threadKeys";
import { useActivePR } from "./app/useActivePR";
import { useAppKeybindings } from "./app/useAppKeybindings";
import { useComposerDrafts } from "./app/useComposerDrafts";
import { useNewThread } from "./app/useNewThread";
import { usePalette } from "./app/usePalette";
import { usePaletteActions } from "./app/usePaletteActions";
import { usePanel } from "./app/usePanel";
import { usePinThread } from "./app/usePinThread";
import { usePresence } from "./app/usePresence";
import { useProjectActions } from "./app/useProjectActions";
import { useProjectView } from "./app/useProjectView";
import { useProviderAuth } from "./app/useProviderAuth";
import { useReadState } from "./app/useReadState";
import { useRenameThread } from "./app/useRenameThread";
import { useRecentSkills } from "./app/useRecentSkills";
import { useScheduleEditor } from "./app/useScheduleEditor";
import { usePruneScrollMemory, useScrollMemory } from "./app/useScrollMemory";
import { useScreens } from "./app/useScreens";
import { useThreadCommands } from "./app/useThreadCommands";
import { useThreadDeletion } from "./app/useThreadDeletion";
import { useThreadHarness } from "./app/useThreadHarness";
import { useThreadOrder } from "./app/useThreadOrder";
import { useThreadSelection, type DraftPreset } from "./app/useThreadSelection";
import { useThreadTitle } from "./app/useThreadTitle";
import { useTranscriptCopy } from "./app/useTranscriptCopy";
import { useWire } from "./app/useWire";
import { AppDialogs } from "./components/AppDialogs";
import { DeleteThreadDialog } from "./components/DeleteThreadDialog";
import { EmptyState } from "./components/EmptyState";
import { SetupScreen, ThemePreviewScreen, ToolsScreen, UsageScreen } from "./components/FullPageScreens";
import { LazyCommandPalette, LazyShortcutsHelp, usePrefetchOverlays } from "./components/Overlays";
import { Sidebar } from "./components/Sidebar";
import { ThreadDraft } from "./components/ThreadDraft";
import { ThreadHeader } from "./components/ThreadHeader";
import { ThreadPanel } from "./components/ThreadPanel";
import { ThreadView } from "./components/ThreadView";
import { isElectronApp } from "./lib/keybindings";
import { loadLastProject } from "./lib/lastProject";
import { skillsScope } from "./lib/skills";
import { cn } from "./lib/utils";
import { useIsDesktop } from "./useMediaQuery";
import { useModHeld } from "./useShortcuts";

export function App() {
  const isDesktop = useIsDesktop();
  const wire = useWire();
  const { clientRef, state, projects, harnesses } = wire;
  // The list as the user should see it: with their renames, pins and moves,
  // before the server has confirmed them, in their order.
  const { threads: renamed, rename: renameThread } = useRenameThread(wire);
  const { threads: withPins, setPinned } = usePinThread(wire, renamed);
  const { threads, move: moveThread } = useThreadOrder(wire, withPins);
  const screens = useScreens();
  const panel = usePanel(wire.stateRef);
  const scroll = useScrollMemory(wire.resume);
  const nav = useThreadSelection({
    wire,
    isDesktop,
    onLeave: panel.reset,
    onForget: scroll.forget,
    onOpenLink: screens.uncover,
  });
  const { activeId, select } = nav;
  usePresence(clientRef, activeId, screens.covering || nav.covering);
  usePruneScrollMemory(scroll, threads);
  const store = useComposerDrafts(threads, activeId);
  const schedule = useScheduleEditor(wire, store);
  const create = useNewThread({ wire, store, select, openSchedule: schedule.openNew });
  const commands = useThreadCommands({ wire, activeId, store, openDiff: panel.openDiff });
  const setThreadUnread = useReadState(wire, activeId);
  const projectActions = useProjectActions(wire);
  const auth = useProviderAuth(wire);
  const copy = useTranscriptCopy(wire);

  const meta = useMemo(() => threads.find((s) => s.id === activeId), [threads, activeId]);
  const harness = useThreadHarness(wire, activeId, meta);
  const recents = useRecentSkills({
    activeId,
    projectId: meta?.projectId,
    state,
    store,
    isDesktop,
    loadComposerItems: commands.loadComposerItems,
  });
  const deletion = useThreadDeletion({ wire, activeId, select });

  const pr = useActivePR(wire, activeId, meta);
  useThreadTitle(activeId, state, meta);

  const accentOf = useCallback(
    (id: string) => harnesses.find((h) => h.id === id)?.accent,
    [harnesses],
  );
  const projectFolders = (id?: string) =>
    projects.find((p) => p.id === id)?.folders.map((f) => f.path) ?? [];

  // The keyboard and the palette. Everything below the early returns is one
  // screen of many, and ⌘K has to work from all of them.
  const projectView = useProjectView(projects);
  const palette = usePalette();
  usePrefetchOverlays();
  const showJumpHints = useModHeld(isElectronApp());
  // One composer at a time, the thread's or the draft's, behind one handle.
  const { composerRef } = recents;
  const turnRunning = state?.phase === "turn";
  const openThread = (id: string) => {
    screens.leaveAll();
    select(id);
  };
  const startNew = (preset?: DraftPreset) => {
    screens.leaveAll();
    nav.startNew(preset);
  };
  const newThreadIn = () => palette.show("newThread");
  // Reachable from over the palette, which would otherwise cover the draft.
  const newThreadHere = () => {
    palette.setOpen(false);
    startNew({ focus: true, projectId: meta?.projectId ?? nav.creating?.projectId });
  };
  const toggleSidebar = () => nav.setSidebarOpen((open) => !open);
  // Settings and the new-project dialog live in the main view's dialogs, so
  // a full-page screen steps aside for them.
  const openSettings = () => {
    screens.uncover();
    screens.setSettings({});
  };
  const copyTranscript = () => void copy.copyAll();

  useAppKeybindings({
    app: {
      paletteOpen: palette.open,
      helpOpen: palette.helpOpen,
      threadOpen: !!state,
      turnRunning,
      panelOpen: panel.open,
    },
    threads,
    projects,
    view: projectView,
    activeId,
    composerRef,
    onOpenThread: openThread,
    onTogglePalette: palette.toggle,
    onNewThreadIn: newThreadIn,
    onNewThreadHere: newThreadHere,
    onCopyTranscript: copyTranscript,
    onStopTurn: commands.cancel,
    onToggleSidebar: toggleSidebar,
    onTogglePanel: panel.toggle,
    onOpenDiff: () => panel.openDiff(),
    onClosePanel: () => {
      if (panel.open) panel.close();
    },
    onSettings: openSettings,
    onToggleHelp: palette.toggleHelp,
  });

  const actions = usePaletteActions({
    meta,
    threadOpen: !!state,
    turnRunning,
    // The full-page screens unmount the composer; Settings is a dialog over it.
    composerOpen: (!!state || !!nav.creating) && !screens.fullPage,
    composerRef,
    onSetPinned: setPinned,
    onSetUnread: setThreadUnread,
    onCopyTranscript: copyTranscript,
    onDelete: deletion.deleteFlow.ask,
    onTogglePanel: panel.toggle,
    onOpenDiff: () => panel.openDiff(),
    onStopTurn: commands.cancel,
    onNewThreadHere: newThreadHere,
    onNewProject: () => {
      screens.uncover();
      screens.setNewProject(true);
    },
    onSettings: openSettings,
    onUsage: () => {
      screens.uncover();
      screens.setShowUsage(true);
    },
    onSkills: () => {
      screens.uncover();
      screens.setTools({});
    },
    onToggleSidebar: toggleSidebar,
    onShortcuts: () => palette.setHelpOpen(true),
  });

  // Rendered on every screen, the full-page ones too.
  const overlays = (
    <>
      <LazyCommandPalette
        // Asked for another page while open: start over on it in place.
        key={palette.nonce}
        open={palette.open}
        initialPage={palette.page}
        onOpenChange={palette.setOpen}
        threads={threads}
        activeId={activeId}
        projects={projects}
        defaultProjectId={meta?.projectId ?? nav.creating?.projectId ?? (loadLastProject() || undefined)}
        actions={actions}
        onSelectThread={openThread}
        onNewThread={startNew}
        onListWorkspaces={projectActions.listWorkspaces}
        onRename={activeId && meta ? (title) => renameThread(activeId, title) : undefined}
      />
      <LazyShortcutsHelp open={palette.helpOpen} onOpenChange={palette.setHelpOpen} />
    </>
  );

  if (screens.themePreview) {
    return (
      <>
        <ThemePreviewScreen />
        {overlays}
      </>
    );
  }

  // First-run setup covers everything, like Usage, but keeps the app's dialogs
  // mounted: its "Sign in" opens the same sign-in flow the thread view does.
  if (screens.setup) {
    return (
      <>
        <SetupScreen harnesses={harnesses} auth={auth} onContinue={screens.leaveSetup} />
        <AppDialogs
          wire={wire}
          screens={screens}
          auth={auth}
          projects={projectActions}
        />
        {overlays}
      </>
    );
  }

  // The Usage page covers the whole viewport, above everything: it answers an
  // account question, and the thread underneath keeps streaming while it is
  // up.
  if (screens.showUsage) {
    return (
      <>
        <UsageScreen
          clientRef={clientRef}
          quotas={wire.quotas}
          onClose={() => screens.setShowUsage(false)}
        />
        {overlays}
      </>
    );
  }

  // The Skills page, the same way. It lists what the open thread can use when
  // there is one, else the project being started in or last used, else only
  // the user's own; its MCP tab asks the open thread's session.
  if (screens.tools) {
    return (
      <>
        <ToolsScreen
          clientRef={clientRef}
          tab={screens.tools.tab}
          projects={projects}
          scope={skillsScope({
            threadId: activeId,
            threadProjectId: meta?.projectId,
            draftProjectId: nav.creating?.projectId,
            lastProjectId: loadLastProject() || undefined,
            projects,
          })}
          onClose={() => screens.setTools(null)}
        />
        {overlays}
      </>
    );
  }

  return (
    <div className="flex h-full overflow-hidden">
      <Sidebar
        threads={threads}
        activeId={activeId}
        status={wire.status}
        open={nav.sidebarOpen}
        onOpenChange={nav.setSidebarOpen}
        onSelect={select}
        onNewThreadIn={newThreadIn}
        onNew={() => nav.startNew()}
        onOpenPalette={() => palette.show("root")}
        projectView={projectView}
        showJumpHints={showJumpHints}
        onDelete={deletion.remove}
        onShowAccess={() => screens.setShowAccess(true)}
        onShowUsage={() => screens.setShowUsage(true)}
        onShowSkills={() => screens.setTools({})}
        onShowSettings={() => screens.setSettings({})}
        accentOf={accentOf}
        projects={projects}
        projectName={(id) => projects.find((p) => p.id === id)?.name}
        projectFolders={projectFolders}
        onSetPinned={setPinned}
        onNewProject={() => screens.setNewProject(true)}
        onSetUnread={setThreadUnread}
        onRename={renameThread}
        onReorder={moveThread}
      />

      <DeleteThreadDialog flow={deletion.deleteFlow} />

      <main
        className={cn(
          "flex min-h-0 min-w-0 flex-1 flex-col",
          // The expanded diff panel takes the whole content area; the main
          // column stays mounted so the transcript keeps its scroll and state.
          panel.open && panel.expanded && "hidden",
        )}
      >
        <ThreadHeader
          sidebarOpen={nav.sidebarOpen}
          onShowSidebar={() => nav.setSidebarOpen(true)}
          state={state}
          activeId={activeId}
          meta={meta}
          onRename={renameThread}
          creating={!!nav.creating}
          isDesktop={isDesktop}
          onSetPinned={setPinned}
          harness={harness}
          copy={copy}
          panel={panel}
          onShowMcp={() => screens.setTools({ tab: "mcp" })}
        />

        {state ? (
          <ThreadView
            state={state}
            activeId={activeId}
            meta={meta}
            harnesses={harnesses}
            scroll={scroll}
            commands={commands}
            harness={harness}
            panel={panel}
            pr={pr}
            recents={recents}
            schedule={schedule}
            store={store}
            authWires={auth.authWires}
            onLogin={auth.openInstanceAuth}
            onForceDelete={deletion.forceDelete}
            onFinish={deletion.deleteFlow.ask}
            readAloud={wire.hasElevenLabsKey}
          />
        ) : nav.creating ? (
          <ThreadDraft
            // A new request is a new draft, on its own values.
            key={nav.creating.nonce}
            preset={nav.creating}
            composerRef={composerRef}
            projects={projects}
            activeProjectId={nav.creating.projectId}
            onProjectChange={nav.pickDraftProject}
            harnesses={harnesses}
            userConfig={wire.userConfig}
            status={wire.status}
            draft={store.drafts[NEW_THREAD] ?? ""}
            onDraftChange={(text) => store.setDraft(NEW_THREAD, text)}
            onStart={create}
            attachments={store.attachments[NEW_THREAD] ?? []}
            onAttachFiles={store.attachToDraft}
            onRemoveAttachment={(key) => store.removeAttachment(NEW_THREAD, key)}
            onListWorkspaces={projectActions.listWorkspaces}
            onListIssues={projectActions.listIssues}
            onListComposerItems={projectActions.listDraftComposerItems}
            onAddProject={() => screens.setNewProject(true)}
            onSettings={(p) => screens.setSettings({ at: { kind: "project", id: p.id } })}
            onRecheck={auth.recheck}
            onLogin={auth.openInstanceAuth}
            onManageProviders={() => screens.setSettings({ at: { kind: "providers" } })}
            dictate={wire.hasElevenLabsKey}
          />
        ) : (
          <EmptyState
            restoring={nav.restoring}
            attaching={!!activeId}
            hasThreads={threads.length > 0}
            onNewThreadIn={newThreadIn}
            onNew={() => nav.startNew()}
          />
        )}
      </main>

      {state && activeId && (
        <ThreadPanel
          clientRef={clientRef}
          threadId={activeId}
          state={state}
          panel={panel}
          pr={pr}
        />
      )}

      <AppDialogs
        wire={wire}
        screens={screens}
        auth={auth}
        projects={projectActions}
      />
      {overlays}
    </div>
  );
}
