import { lazy, Suspense } from "react";

import type { LabelActions } from "~/app/useLabelActions";
import type { ProjectActions } from "~/app/useProjectActions";
import type { ProviderAuth } from "~/app/useProviderAuth";
import type { Screens } from "~/app/useScreens";
import type { Wire } from "~/app/useWire";

import { AccessPanel } from "./Access";
import { LabelManager } from "./LabelManager";

const NewProject = lazy(() => import("./NewProject").then((m) => ({ default: m.NewProject })));
const SettingsScreen = lazy(() =>
  import("./SettingsScreen").then((m) => ({ default: m.SettingsScreen })),
);
// The sign-in dialog carries xterm; it stays out of the first load like the Panel does.
const LoginDialog = lazy(() => import("./LoginDialog").then((m) => ({ default: m.LoginDialog })));
const InstanceAuthDialog = lazy(() => import("./AuthFlowDialog"));

/** The dialogs and sheets the app opens over everything else. */
export function AppDialogs({
  wire,
  screens,
  labels,
  auth,
  projects,
}: {
  wire: Wire;
  screens: Screens;
  labels: LabelActions;
  auth: ProviderAuth;
  projects: ProjectActions;
}) {
  const { clientRef, access, setAccess } = wire;
  const { settings, setSettings, setNewProject } = screens;
  const { authInstance, setAuthInstance, loginInstance, setLoginInstance, recheck } = auth;
  return (
    <>
      {screens.manageLabels && (
        <LabelManager
          labels={wire.labels}
          onCreate={labels.createLabel}
          onSave={labels.saveLabel}
          onDelete={labels.deleteLabel}
          onClose={() => screens.setManageLabels(false)}
        />
      )}

      {screens.showAccess && access && (
        <AccessPanel
          access={access}
          onEnableHTTPS={async () => {
            const res = await clientRef.current!.command("enable_https", {});
            if (res?.access) setAccess(res.access);
          }}
          onDisableHTTPS={async () => {
            const res = await clientRef.current!.command("disable_https", {});
            if (res?.access) setAccess(res.access);
          }}
          onClose={() => screens.setShowAccess(false)}
        />
      )}

      {authInstance && (
        <Suspense fallback={null}>
          <InstanceAuthDialog
            wires={auth.authWires}
            instanceId={authInstance}
            instanceName={auth.instanceName(authInstance)}
            onOpenTerminal={() => {
              setAuthInstance(null);
              setLoginInstance(authInstance);
            }}
            onClose={() => setAuthInstance(null)}
          />
        </Suspense>
      )}
      {loginInstance && (
        <Suspense fallback={null}>
          <LoginDialog
            instanceId={loginInstance}
            name={auth.instanceName(loginInstance)}
            onEnded={recheck}
            onClose={() => {
              setLoginInstance(null);
              recheck();
            }}
          />
        </Suspense>
      )}
      {screens.newProject && (
        <Suspense fallback={null}>
          <NewProject
            onCreate={projects.createProject}
            listRepos={projects.listRepos}
            onClose={() => setNewProject(false)}
          />
        </Suspense>
      )}
      {settings && (
        <Suspense fallback={null}>
          <SettingsScreen
            at={settings.at}
            projects={wire.projects}
            harnesses={wire.harnesses}
            userConfig={wire.userConfig}
            threadCounts={projects.threadCounts}
            onSaveUserConfig={wire.saveUserConfig}
            providers={{
              wires: auth.authWires,
              onOpenTerminal: setLoginInstance,
              onRecheck: recheck,
            }}
            project={{
              onSave: projects.saveProject,
              onAddFolder: projects.addFolder,
              onRemoveFolder: projects.removeFolder,
              listRepos: projects.listRepos,
              onDelete: projects.deleteProject,
            }}
            onAddProject={() => {
              setSettings(null);
              setNewProject(true);
            }}
            onClose={() => setSettings(null)}
          />
        </Suspense>
      )}
    </>
  );
}
