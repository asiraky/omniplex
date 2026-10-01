import { useCallback, useMemo } from "react";

import { NEW_THREAD } from "./app/threadKeys";
import { useActivePR } from "./app/useActivePR";
import { useComposerDrafts } from "./app/useComposerDrafts";
import { useLabelActions } from "./app/useLabelActions";
import { useNewThread } from "./app/useNewThread";
import { usePanel } from "./app/usePanel";
import { useProjectActions } from "./app/useProjectActions";
import { useProviderAuth } from "./app/useProviderAuth";
import { useReadState } from "./app/useReadState";
import { useRecentSkills } from "./app/useRecentSkills";
import { useScheduleEditor } from "./app/useScheduleEditor";
import { usePruneScrollMemory, useScrollMemory } from "./app/useScrollMemory";
import { useScreens } from "./app/useScreens";
import { useThreadCommands } from "./app/useThreadCommands";
import { useThreadDeletion } from "./app/useThreadDeletion";
import { useThreadHarness } from "./app/useThreadHarness";
import { useThreadSelection } from "./app/useThreadSelection";
import { useThreadTitle } from "./app/useThreadTitle";
import { useTranscriptCopy } from "./app/useTranscriptCopy";
import { useWire } from "./app/useWire";
import { AppDialogs } from "./components/AppDialogs";
import { DeleteThreadDialog } from "./components/DeleteThreadDialog";
import { EmptyState } from "./components/EmptyState";
import { SetupScreen, ThemePreviewScreen, ToolsScreen, UsageScreen } from "./components/FullPageScreens";
import { Sidebar } from "./components/Sidebar";
import { ThreadDraft } from "./components/ThreadDraft";
import { ThreadHeader } from "./components/ThreadHeader";
import { ThreadPanel } from "./components/ThreadPanel";
import { ThreadView } from "./components/ThreadView";
import { loadLastProject } from "./lib/lastProject";
import { skillsScope } from "./lib/skills";
import { cn } from "./lib/utils";
import { useIsDesktop } from "./useMediaQuery";

export function App() {
  const isDesktop = useIsDesktop();
  const wire = useWire();
  const { clientRef, state, threads, projects, harnesses, labels } = wire;
  const screens = useScreens();
  const panel = usePanel(wire.stateRef);
  const scroll = useScrollMemory(wire.resume);
  const nav = useThreadSelection({
    wire,
    isDesktop,
    onLeave: panel.reset,
    onForget: scroll.forget,
  });
  const { activeId, select } = nav;
  usePruneScrollMemory(scroll, threads);
  const store = useComposerDrafts(threads, activeId);
  const schedule = useScheduleEditor(wire, store);
  const create = useNewThread({ wire, store, select, openSchedule: schedule.openNew });
  const commands = useThreadCommands({ wire, activeId, store, openDiff: panel.openDiff });
  const setThreadUnread = useReadState(wire, activeId);
  const labelActions = useLabelActions(wire);
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

  if (screens.themePreview) return <ThemePreviewScreen />;

  // First-run setup covers everything, like Usage, but keeps the app's dialogs
  // mounted: its "Sign in" opens the same sign-in flow the thread view does.
  if (screens.setup) {
    return (
      <>
        <SetupScreen harnesses={harnesses} auth={auth} onContinue={screens.leaveSetup} />
        <AppDialogs
          wire={wire}
          screens={screens}
          labels={labelActions}
          auth={auth}
          projects={projectActions}
        />
      </>
    );
  }

  // The Usage page covers the whole viewport, above everything: it answers an
  // account question, and the thread underneath keeps streaming while it is
  // up.
  if (screens.showUsage) {
    return (
      <UsageScreen
        clientRef={clientRef}
        quotas={wire.quotas}
        onClose={() => screens.setShowUsage(false)}
      />
    );
  }

  // The Skills page, the same way. It lists what the open thread can use when
  // there is one, else the project being started in or last used, else only
  // the user's own; its MCP tab asks the open thread's session.
  if (screens.tools) {
    return (
      <ToolsScreen
        clientRef={clientRef}
        tab={screens.tools.tab}
        scope={skillsScope({
          threadId: activeId,
          threadProjectId: meta?.projectId,
          draftProjectId: nav.creating?.projectId,
          lastProjectId: loadLastProject() || undefined,
          projects,
        })}
        onClose={() => screens.setTools(null)}
      />
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
        onNew={nav.startNew}
        onDelete={deletion.remove}
        onShowAccess={() => screens.setShowAccess(true)}
        onShowUsage={() => screens.setShowUsage(true)}
        onShowSkills={() => screens.setTools({})}
        onShowSettings={() => screens.setSettings({})}
        accentOf={accentOf}
        projects={projects}
        projectName={(id) => projects.find((p) => p.id === id)?.name}
        projectFolders={projectFolders}
        labels={labels}
        onSetLabel={labelActions.setThreadLabel}
        onManageLabels={() => screens.setManageLabels(true)}
        onNewProject={() => screens.setNewProject(true)}
        onSetUnread={setThreadUnread}
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
          creating={!!nav.creating}
          isDesktop={isDesktop}
          labels={{
            labels,
            onSetLabel: labelActions.setThreadLabel,
            onManage: () => screens.setManageLabels(true),
          }}
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
            onLogin={auth.openInstanceAuth}
            onForceDelete={deletion.forceDelete}
            onFinish={deletion.deleteFlow.ask}
          />
        ) : nav.creating ? (
          <ThreadDraft
            projects={projects}
            activeProjectId={nav.creating.projectId}
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
          />
        ) : (
          <EmptyState
            restoring={nav.restoring}
            attaching={!!activeId}
            hasThreads={threads.length > 0}
            onNew={nav.startNew}
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
        labels={labelActions}
        auth={auth}
        projects={projectActions}
      />
    </div>
  );
}
