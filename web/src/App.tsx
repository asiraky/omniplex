import { useCallback, useMemo } from "react";

import { NEW_THREAD } from "./app/threadKeys";
import { ThreadStoreContext, useThread } from "./app/threadStore";
import { useActivePR } from "./app/useActivePR";
import { useAddToChat, useAppendToChat } from "./app/useAddToChat";
import { useAppKeybindings } from "./app/useAppKeybindings";
import { useBackgroundAlerts } from "./app/useBackgroundAlerts";
import { useComposerDrafts } from "./app/useComposerDrafts";
import { useCompareThreads, useNewThread } from "./app/useNewThread";
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
import { useCopy } from "./lib/clipboard";
import { isElectronApp } from "./lib/keybindings";
import { loadLastProject } from "./lib/lastProject";
import { skillsScope } from "./lib/skills";
import { threadUrl } from "./lib/threadLinks";
import { cn } from "./lib/utils";
import { useGuardFileDrops } from "./useFileDrop";
import { useIsDesktop } from "./useMediaQuery";
import type { ThreadState } from "./protocol";
import { useModHeld } from "./useShortcuts";

export function App() {
  const isDesktop = useIsDesktop();
  const wire = useWire();
  // Not the attached thread's state: only whether there is one. The state
  // itself is in a store each part of the page selects its own slice of, so a
  // frame off the wire (a job's progress, a usage tick) rerenders what it
  // changed and not the app.
  const { clientRef, threadStore, attached, projects, harnesses } = wire;
  // The list as the user should see it: with their renames, pins and moves,
  // before the server has confirmed them, in their order.
  const { threads: renamed, rename: renameThread } = useRenameThread(wire);
  const { threads: withPins, setPinned } = usePinThread(wire, renamed);
  const { threads, move: moveThread } = useThreadOrder(wire, withPins);
  const screens = useScreens();
  const panel = usePanel(threadStore);
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
  const store = useComposerDrafts(threads);
  const schedule = useScheduleEditor(wire, store);
  const commands = useThreadCommands({ wire, activeId, store, openDiff: panel.openDiff });
  const create = useNewThread({
    wire,
    store,
    select,
    openSchedule: schedule.openNew,
    sendWhenUploaded: commands.sendWhenUploaded,
  });
  const compare = useCompareThreads({ wire, store, select });
  const setThreadUnread = useReadState(wire, activeId);
  const projectActions = useProjectActions(wire);
  const auth = useProviderAuth(wire);
  const copy = useTranscriptCopy(wire);
  // One copier for every "Copy thread link": the header, the palette and the
  // row menu. Kept stable for the sidebar's memo; `copy` already is.
  const { copied: linkCopied, copy: copyLink } = useCopy();
  const copyThreadLink = useCallback((id: string) => void copyLink(threadUrl(id)), [copyLink]);
  // A drop on a sidebar row stages the files on that thread's composer and
  // opens it, the way a tap on the row would, so its upload chips and the
  // message to go with them are in front of the user. The thread already open
  // only takes the files: select leaves it as it is.
  const { attachFiles } = store;
  const dropOnThread = useCallback(
    (id: string, files: File[]) => {
      attachFiles(id, files);
      select(id);
    },
    [attachFiles, select],
  );

  const meta = useMemo(() => threads.find((s) => s.id === activeId), [threads, activeId]);
  const harness = useThreadHarness(wire, activeId, meta);
  const recents = useRecentSkills({
    activeId,
    projectId: meta?.projectId,
    threadStore,
    store,
    isDesktop,
    loadComposerItems: commands.loadComposerItems,
  });
  const deletion = useThreadDeletion({ wire, activeId, select });

  const pr = useActivePR(wire, activeId, meta);
  useThreadTitle(activeId, threadStore, meta);

  const accentOf = useCallback(
    (id: string) => harnesses.find((h) => h.id === id)?.accent,
    [harnesses],
  );
  // Kept to the projects list's identity, like accentOf: the sidebar is
  // memoised, and a fresh lookup on every App render would re-render it.
  const projectName = useCallback(
    (id?: string) => projects.find((p) => p.id === id)?.name,
    [projects],
  );
  const projectFolders = useCallback(
    (id?: string) => projects.find((p) => p.id === id)?.folders.map((f) => f.path) ?? [],
    [projects],
  );

  // The keyboard and the palette. Everything below the early returns is one
  // screen of many, and ⌘K has to work from all of them.
  const projectView = useProjectView(projects);
  const palette = usePalette();
  usePrefetchOverlays();
  // A file dropped where nothing takes it would otherwise replace the app.
  useGuardFileDrops();
  const showJumpHints = useModHeld(isElectronApp());
  // One composer at a time, the thread's or the draft's, behind one handle.
  const { composerRef } = recents;
  const addToChat = useAddToChat({ activeId, store, panel, composerRef });
  const appendToChat = useAppendToChat({ activeId, store });
  const turnRunning = useThread(threadStore, isInTurn);
  const openThread = (id: string) => {
    screens.leaveAll();
    select(id);
  };
  useBackgroundAlerts({
    threads: wire.threads,
    loaded: wire.threadsLoaded,
    activeId,
    covered: screens.covering || nav.covering,
    open: openThread,
  });
  const startNew = (preset?: DraftPreset) => {
    screens.leaveAll();
    nav.startNew(preset);
  };
  // The sidebar's, kept stable for its memo like the screens' openers.
  const { show: showPalette } = palette;
  const { startNew: startFresh } = nav;
  const newThreadIn = useCallback(() => showPalette("newThread"), [showPalette]);
  const openPaletteRoot = useCallback(() => showPalette("root"), [showPalette]);
  const startBlank = useCallback(() => startFresh(), [startFresh]);
  // Reachable from over the palette, which would otherwise cover the draft.
  const newThreadHere = () => {
    palette.setOpen(false);
    startNew({ focus: true, projectId: meta?.projectId ?? nav.creating?.projectId });
  };
  const toggleSidebar = () => nav.setSidebarOpen((open) => !open);
  // Setup and the new-thread draft both send a driver with no account here;
  // null lets Providers ask which driver.
  const addAccount = (harnessId: string | null) => screens.setSettings({ at: { kind: "providers", addFor: harnessId } });
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
      threadOpen: attached,
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
    threadOpen: attached,
    turnRunning,
    // The full-page screens unmount the composer; Settings is a dialog over it.
    composerOpen: (attached || !!nav.creating) && !screens.fullPage,
    composerRef,
    onSetPinned: setPinned,
    onSetUnread: setThreadUnread,
    onCopyTranscript: copyTranscript,
    onCopyLink: copyThreadLink,
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
        <SetupScreen
          harnesses={harnesses}
          auth={auth}
          onAddAccount={addAccount}
          onContinue={screens.leaveSetup}
        />
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
    <ThreadStoreContext.Provider value={threadStore}>
      <div className="flex h-full overflow-hidden">
        <Sidebar
          threads={threads}
          activeId={activeId}
          status={wire.status}
          open={nav.sidebarOpen}
          onOpenChange={nav.setSidebarOpen}
          onSelect={select}
          onNewThreadIn={newThreadIn}
          onNew={startBlank}
          onOpenPalette={openPaletteRoot}
          projectView={projectView}
          showJumpHints={showJumpHints}
          onDelete={deletion.remove}
          onShowAccess={screens.openAccess}
          onShowUsage={screens.openUsage}
          onShowSkills={screens.openSkills}
          onShowSettings={screens.openSettings}
          accentOf={accentOf}
          projects={projects}
          projectName={projectName}
          projectFolders={projectFolders}
          onSetPinned={setPinned}
          onNewProject={screens.openNewProject}
          onSetUnread={setThreadUnread}
          onRename={renameThread}
          onCopyLink={copyThreadLink}
          onDropFiles={dropOnThread}
          onReorder={moveThread}
          // Only with a thread or a draft behind the drawer: otherwise it is
          // the landing screen, and Back closing it would show nothing.
          backDismiss={!!activeId || !!nav.creating}
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
            activeId={activeId}
            meta={meta}
            onRename={renameThread}
            creating={!!nav.creating}
            isDesktop={isDesktop}
            onSetPinned={setPinned}
            harness={harness}
            copy={copy}
            link={{ copied: linkCopied, copy: copyThreadLink }}
            panel={panel}
            onShowMcp={() => screens.setTools({ tab: "mcp" })}
          />

          {attached ? (
            <ThreadView
              activeId={activeId}
              meta={meta}
              threads={threads}
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
              status={wire.status}
              turnStatus={wire.turnStatus}
              // Not on a phone: the cursor there would raise the keyboard over the new draft.
              onSendAndNewThread={(preset) => startNew({ ...preset, focus: isDesktop })}
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
              drafts={store.draftStore}
              onStart={create}
              onCompare={compare}
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
              onAddAccount={addAccount}
              dictate={wire.hasElevenLabsKey}
            />
          ) : (
            <EmptyState
              restoring={nav.restoring}
              attaching={!!activeId}
              hasThreads={threads.length > 0}
              onNewThreadIn={newThreadIn}
              onNew={startBlank}
            />
          )}
        </main>

        {attached && activeId && (
          <ThreadPanel
            clientRef={clientRef}
            threadId={activeId}
            panel={panel}
            pr={pr}
            onAddToChat={addToChat}
            onAddComment={appendToChat}
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
    </ThreadStoreContext.Provider>
  );
}

const isInTurn = (state: ThreadState | null) => state?.phase === "turn";
