import { lazy, Suspense, useCallback, useMemo, useState } from "react";

import { NEW_THREAD } from "./app/threadKeys";
import { ThreadStoreContext, useThread } from "./app/threadStore";
import { useActivePR } from "./app/useActivePR";
import { useAddToChat, useAppendToChat, useBoxAddToChat } from "./app/useAddToChat";
import { currentAgent, useFollowAgentThread } from "./app/useAgents";
import { useAppKeybindings } from "./app/useAppKeybindings";
import { useBackgroundAlerts } from "./app/useBackgroundAlerts";
import { useComposerDrafts } from "./app/useComposerDrafts";
import { useFindBar } from "./app/useFindBar";
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
import { ArchiveAgentDialog, RestartAgentDialog } from "./components/agents/AgentDialogs";
import type { AgentPageAction } from "./components/agents/AgentPage";
import { NewAgentThreadDialog } from "./components/agents/NewAgentThreadDialog";
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
import { requestSeqJump } from "./components/transcript/seqJump";
import type { Command } from "./lib/agentApi";
import { AgentNavContext, type AgentFocus, type AgentNav, type AgentTab } from "./lib/agentNav";
import { useCopy } from "./lib/clipboard";
import { navigate } from "./lib/history";
import { isElectronApp } from "./lib/keybindings";
import { loadLastProject } from "./lib/lastProject";
import { skillsScope } from "./lib/skills";
import { threadPath, threadUrl } from "./lib/threadLinks";
import { toast } from "./lib/toast";
import { cn } from "./lib/utils";
import { useGuardFileDrops } from "./useFileDrop";
import { useIsDesktop } from "./useMediaQuery";
import type { Agent, ThreadState } from "./protocol";
import { useModHeld } from "./useShortcuts";

// Most sessions never open one, so it stays out of the first load.
const AssistantDraft = lazy(() =>
  import("./components/AssistantDraft").then((m) => ({ default: m.AssistantDraft })),
);

// An agent's page carries the revision editors and the log; most sessions
// never open one.
const AgentPage = lazy(() => import("./components/agents/AgentPage").then((m) => ({ default: m.AgentPage })));
const NewAgent = lazy(() => import("./components/agents/NewAgent").then((m) => ({ default: m.NewAgent })));

// It carries xterm, which only a terminal needs.
const BoxTerminalScreen = lazy(() =>
  import("./components/BoxTerminalScreen").then((m) => ({ default: m.BoxTerminalScreen })),
);

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
  // A notification or Back opening a thread clears what covers it, an agent
  // page too. select leaves the thread already open alone, URL included, so
  // coming to it from an agent page's URL the URL has to be put right here.
  const { uncover, dropPage } = screens;
  const { activeRef } = wire;
  const openedLink = useCallback(
    (id: string) => {
      uncover();
      dropPage();
      if (id === activeRef.current && window.location.pathname !== threadPath(id)) navigate(threadPath(id));
    },
    [uncover, dropPage, activeRef],
  );
  const nav = useThreadSelection({
    wire,
    isDesktop,
    onLeave: panel.reset,
    onForget: scroll.forget,
    onOpenLink: openedLink,
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
  // The thread that delegated the open one, while it is still listed: the
  // header offers the way back to it.
  const parentId = meta?.parentId;
  const parent = useMemo(
    () => (parentId && parentId !== activeId ? threads.find((s) => s.id === parentId) : undefined),
    [threads, parentId, activeId],
  );
  const { agents, agentApi: agentsApi } = wire;
  const current = useMemo(() => currentAgent(agents, activeId, meta), [agents, activeId, meta]);
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

  // Leaving an agent page: for a thread, else the draft it was opened over,
  // else nothing (on a phone, the sidebar). closePage makes it a Back when
  // that is where the page was opened from.
  const { page, openPage, closePage } = screens;
  const { setSidebarOpen, startNew: startFresh } = nav;
  const creatingDraft = !!nav.creating;
  const leavePage = useCallback(
    (threadId: string | null, { replace = false }: { replace?: boolean } = {}) => {
      if (threadId) {
        closePage(threadPath(threadId), { replace });
        select(threadId, { history: false });
      } else if (creatingDraft) {
        closePage("/", { draft: true, replace });
      } else {
        closePage("/", { replace });
        if (!isDesktop) setSidebarOpen(true);
      }
    },
    [closePage, select, creatingDraft, isDesktop, setSidebarOpen],
  );
  // A thread from anywhere: off an agent page when one is up, else a pick.
  // Kept stable for the sidebar's memo.
  const pageOpen = !!page;
  // Either lands where the thread was left, so a jump into a thread asked for
  // before (seqJump.ts) and not yet landed is dropped.
  const showThread = useCallback(
    (id: string) => {
      requestSeqJump(id);
      return pageOpen ? leavePage(id) : select(id);
    },
    [pageOpen, leavePage, select],
  );
  const openThread = (id: string) => {
    requestSeqJump(id);
    if (pageOpen) {
      uncover();
      leavePage(id);
      return;
    }
    screens.leaveAll();
    select(id);
  };
  const openAgent = useCallback(
    (agentId: string, tab: AgentTab = "prompt", focus?: AgentFocus) => {
      openPage({ kind: "agent", agentId, tab, ...(focus ? { focus } : {}) });
      if (!isDesktop) setSidebarOpen(false);
    },
    [openPage, isDesktop, setSidebarOpen],
  );
  const newAgent = useCallback(
    (projectId?: string) => {
      openPage({ kind: "new", ...(projectId ? { projectId } : {}) });
      if (!isDesktop) setSidebarOpen(false);
    },
    [openPage, isDesktop, setSidebarOpen],
  );
  // An agent row goes to what the agent is for, its live thread; with none,
  // to its page.
  const openAgentRow = useCallback(
    (a: Agent) => (a.threadId ? showThread(a.threadId) : openAgent(a.id, "prompt")),
    [showThread, openAgent],
  );
  const agentNav = useMemo<AgentNav>(
    () => ({
      current,
      threadId: activeId ?? undefined,
      openAgent,
      // After the open, which drops any ask still standing.
      openThread: (id, seq) => {
        showThread(id);
        requestSeqJump(id, seq);
      },
    }),
    [current, activeId, openAgent, showThread],
  );
  // New thread and a server-side swap both give an agent a new live thread;
  // the user watching the old one goes with it. On an agent page the URL is
  // the page's, so only the attachment moves.
  useFollowAgentThread(agents, activeId, (id) => {
    select(id, { history: false });
    if (!page) navigate(threadPath(id), { replace: true });
  });
  // The agent dialogs, opened from the sidebar's rows and from the page.
  const [agentDialog, setAgentDialog] = useState<{ kind: AgentPageAction; agent: Agent } | null>(null);
  const agentAction = useCallback((agent: Agent, kind: AgentPageAction) => setAgentDialog({ kind, agent }), []);
  const command = useCallback<Command>(
    (name, args) => {
      const client = clientRef.current;
      if (!client) return Promise.reject(new Error("Not connected."));
      return client.command(name, args);
    },
    [clientRef],
  );
  // Archived from its page or its thread, the agent leaves nothing to show:
  // the user lands on a new draft in its project, in the page's entry.
  const afterArchive = (agent: Agent) => {
    const onIt = (page?.kind === "agent" && page.agentId === agent.id) || (!!activeId && activeId === agent.threadId);
    if (!onIt) return;
    if (page) closePage("/", { draft: true, replace: true });
    startFresh({ projectId: agent.projectId }, { history: !page });
  };
  useBackgroundAlerts({
    threads: wire.threads,
    loaded: wire.threadsLoaded,
    activeId,
    covered: screens.covering || nav.covering,
    open: openThread,
  });
  // The box terminal. Opened from anywhere, a phone's sheet steps aside for
  // it; the sidebar's own row closes the sheet itself.
  const { openBoxTerminal, closeBoxTerminal } = screens;
  const openTerminal = () => {
    openBoxTerminal();
    if (!isDesktop) setSidebarOpen(false);
  };
  // On a phone with nothing open behind it, the terminal was reached from
  // the drawer, the landing screen, so leaving it by any way goes back there.
  const [terminalWasOpen, setTerminalWasOpen] = useState(screens.boxTerminal);
  if (terminalWasOpen !== screens.boxTerminal) {
    setTerminalWasOpen(screens.boxTerminal);
    if (!screens.boxTerminal && !isDesktop && !activeId && !nav.creating && !screens.page) setSidebarOpen(true);
  }
  const boxAddToChat = useBoxAddToChat({
    activeId,
    attached,
    threads,
    store,
    composerRef,
    onClose: closeBoxTerminal,
    onOpenThread: openThread,
  });
  const startNew = (preset?: DraftPreset) => {
    screens.leaveAll();
    nav.startNew(preset);
  };
  // The sidebar's, kept stable for its memo like the screens' openers.
  const { show: showPalette } = palette;
  const newThreadIn = useCallback(() => showPalette("newThread"), [showPalette]);
  const openPaletteRoot = useCallback(() => showPalette("root"), [showPalette]);
  // A draft from the sidebar puts an agent page away; its own push is the
  // draft's entry.
  const startBlank = useCallback(() => {
    dropPage();
    startFresh();
  }, [dropPage, startFresh]);
  // Not focused on a phone, where the cursor would raise the keyboard over the draft.
  const startAssistant = useCallback(
    (isPrivate: boolean) => {
      dropPage();
      startFresh({ assistant: true, private: isPrivate, focus: isDesktop });
    },
    [dropPage, startFresh, isDesktop],
  );
  // Reachable from over the palette, which would otherwise cover the draft.
  const newThreadHere = () => {
    palette.setOpen(false);
    startNew({ focus: true, projectId: meta?.projectId ?? nav.creating?.projectId });
  };
  const newAssistant = (isPrivate = false) => {
    palette.setOpen(false);
    screens.leaveAll();
    startAssistant(isPrivate);
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
  // The open thread's find bar: the header's phone menu, the palette and
  // Ctrl/⌘+F all open it, and a thread switch closes it.
  const find = useFindBar(activeId);
  const transcriptShown = attached && !screens.fullPage;

  useAppKeybindings({
    app: {
      paletteOpen: palette.open,
      helpOpen: palette.helpOpen,
      threadOpen: attached,
      transcriptShown,
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
    onNewAssistant: () => newAssistant(),
    onCopyTranscript: copyTranscript,
    onFindInThread: find.show,
    onStopTurn: commands.cancel,
    onToggleSidebar: toggleSidebar,
    onTogglePanel: panel.toggle,
    onToggleTerminal: () => (screens.boxTerminal ? closeBoxTerminal() : openTerminal()),
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
    transcriptShown,
    turnRunning,
    // The full-page screens unmount the composer; Settings is a dialog over it.
    composerOpen: (attached || !!nav.creating) && !screens.fullPage,
    composerRef,
    onSetPinned: setPinned,
    onSetUnread: setThreadUnread,
    onCopyTranscript: copyTranscript,
    onFindInThread: find.show,
    onCopyLink: copyThreadLink,
    onDelete: deletion.deleteFlow.ask,
    onTogglePanel: panel.toggle,
    onOpenDiff: () => panel.openDiff(),
    onStopTurn: commands.cancel,
    restart: {
      can: harness.canRestart,
      pending: harness.restartPending,
      restarting: harness.restarting,
      onRestart: harness.restartThread,
      onCancel: harness.cancelRestart,
    },
    onNewThreadHere: newThreadHere,
    onNewAssistant: newAssistant,
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
    onTerminal: openTerminal,
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

  const pageAgent = page?.kind === "agent" ? agents.find((a) => a.id === page.agentId) : undefined;
  // The agent the sidebar marks: the page's, else the attached thread's.
  const activeAgentId = page ? (page.kind === "agent" ? page.agentId : null) : (current?.id ?? null);
  const dialogAgent = agentDialog && (agents.find((a) => a.id === agentDialog.agent.id) ?? agentDialog.agent);

  return (
    <ThreadStoreContext.Provider value={threadStore}>
      <AgentNavContext.Provider value={agentNav}>
        <div className="flex h-full overflow-hidden">
          <Sidebar
            threads={threads}
            activeId={activeId}
            status={wire.status}
            open={nav.sidebarOpen}
            onOpenChange={nav.setSidebarOpen}
            onSelect={showThread}
            onNewThreadIn={newThreadIn}
            onNew={startBlank}
            onNewAssistant={startAssistant}
            onOpenPalette={openPaletteRoot}
            projectView={projectView}
            showJumpHints={showJumpHints}
            onDelete={deletion.remove}
            onShowAccess={screens.openAccess}
            onShowUsage={screens.openUsage}
            onShowSkills={screens.openSkills}
            onShowSettings={screens.openSettings}
            terminalOpen={screens.boxTerminal}
            onOpenTerminal={openBoxTerminal}
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
            // Only with a thread, a draft or a page behind the drawer:
            // otherwise it is the landing screen, and Back closing it would
            // show nothing.
            backDismiss={!!activeId || !!nav.creating || pageOpen}
            agents={agents}
            activeAgentId={activeAgentId}
            pageOpen={pageOpen}
            onOpenAgentRow={openAgentRow}
            onOpenAgent={openAgent}
            onAgentAction={agentAction}
            onNewAgent={newAgent}
          />

          <DeleteThreadDialog flow={deletion.deleteFlow} />

          <main
            className={cn(
              "flex min-h-0 min-w-0 flex-1 flex-col",
              // The expanded diff panel takes the whole content area, and the
              // box terminal the main pane; the main column stays mounted so
              // the transcript keeps its scroll and state.
              ((panel.open && panel.expanded) || screens.boxTerminal || pageOpen) && "hidden",
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
              onFind={find.show}
              onClose={deletion.deleteFlow.ask}
              parent={parent}
              onOpenThread={showThread}
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
                // Read-aloud caches audio on disk, which a private chat must not.
                readAloud={wire.hasElevenLabsKey && !meta?.private}
                status={wire.status}
                turnStatus={wire.turnStatus}
                // Not on a phone: the cursor there would raise the keyboard over the new draft.
                onSendAndNewThread={(preset) => startNew({ ...preset, focus: isDesktop })}
                find={find}
                onOpenThread={showThread}
              />
            ) : nav.creating?.assistant ? (
              <Suspense fallback={null}>
                <AssistantDraft
                  key={nav.creating.nonce}
                  preset={nav.creating}
                  composerRef={composerRef}
                  harnesses={harnesses}
                  userConfig={wire.userConfig}
                  status={wire.status}
                  drafts={store.draftStore}
                  onStart={create}
                  attachments={store.attachments[NEW_THREAD] ?? []}
                  onAttachFiles={store.attachToDraft}
                  onRemoveAttachment={(key) => store.removeAttachment(NEW_THREAD, key)}
                  onRecheck={auth.recheck}
                  onLogin={auth.openInstanceAuth}
                  onManageProviders={() => screens.setSettings({ at: { kind: "providers" } })}
                  onAddAccount={addAccount}
                  dictate={wire.hasElevenLabsKey}
                />
              </Suspense>
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

          {screens.boxTerminal && (
            <Suspense fallback={null}>
              <BoxTerminalScreen
                sidebarOpen={nav.sidebarOpen}
                onShowSidebar={() => setSidebarOpen(true)}
                onClose={closeBoxTerminal}
                onAddToChat={boxAddToChat}
              />
            </Suspense>
          )}

          {/* Under the box terminal, which can be opened over it, and gone
              while the terminal is up: closing the terminal comes back here. */}
          {page && !screens.boxTerminal && (
            <Suspense fallback={null}>
              {page.kind === "agent" ? (
                <AgentPage
                  agent={pageAgent}
                  loaded={wire.threadsLoaded}
                  tab={page.tab}
                  focus={page.focus}
                  api={agentsApi}
                  projects={projects}
                  harnesses={harnesses}
                  sidebarOpen={nav.sidebarOpen}
                  onShowSidebar={() => setSidebarOpen(true)}
                  // To the agent's live thread, else the one it was opened over.
                  onBack={() => leavePage(pageAgent?.threadId ?? activeId)}
                  onOpenThread={showThread}
                  onTab={(tab) => openPage({ kind: "agent", agentId: page.agentId, tab })}
                  onAction={agentAction}
                />
              ) : (
                <NewAgent
                  projects={projects}
                  harnesses={harnesses}
                  userConfig={wire.userConfig}
                  projectId={page.projectId}
                  api={agentsApi}
                  sidebarOpen={nav.sidebarOpen}
                  onShowSidebar={() => setSidebarOpen(true)}
                  onCancel={() => leavePage(activeId)}
                  // The form is done with either way: its entry becomes the
                  // thread, or the new agent's page.
                  onCreated={(agent, threadId) => {
                    if (threadId) {
                      leavePage(threadId, { replace: true });
                      return;
                    }
                    openPage({ kind: "agent", agentId: agent.id, tab: "prompt" }, { replace: true });
                    toast.info("Agent created. No thread yet.");
                  }}
                />
              )}
            </Suspense>
          )}

          {agentDialog?.kind === "newThread" && dialogAgent && (
            <NewAgentThreadDialog agent={dialogAgent} command={command} onClose={() => setAgentDialog(null)} />
          )}
          {agentDialog?.kind === "restart" && dialogAgent && (
            <RestartAgentDialog agent={dialogAgent} command={command} onClose={() => setAgentDialog(null)} />
          )}
          {agentDialog?.kind === "archive" && dialogAgent && (
            <ArchiveAgentDialog
              agent={dialogAgent}
              api={agentsApi}
              open
              onOpenChange={(open) => !open && setAgentDialog(null)}
              onArchived={() => afterArchive(dialogAgent)}
            />
          )}

          {/* Put away rather than unmounted under the box terminal and the agent
              pages: the thread's own terminals keep their sockets for the way back. */}
          {attached && activeId && (
            <div className={screens.boxTerminal || pageOpen ? "hidden" : "contents"}>
              <ThreadPanel
                clientRef={clientRef}
                threadId={activeId}
                panel={panel}
                pr={pr}
                onAddToChat={addToChat}
                onAddComment={appendToChat}
                threads={threads}
                onOpenThread={showThread}
              />
            </div>
          )}

          <AppDialogs
            wire={wire}
            screens={screens}
            auth={auth}
            projects={projectActions}
          />
          {overlays}
        </div>
      </AgentNavContext.Provider>
    </ThreadStoreContext.Provider>
  );
}

const isInTurn = (state: ThreadState | null) => state?.phase === "turn";
