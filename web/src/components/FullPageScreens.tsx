import { lazy, Suspense, useCallback, useMemo, type RefObject } from "react";

import type { ProviderAuth } from "~/app/useProviderAuth";
import type { Client } from "~/client";
import { fetchSetup } from "~/lib/setup";
import type { SkillsScope } from "~/lib/skills";
import type { ToolsTab } from "~/lib/toolsTab";
import type {
  AuthFlowEvent,
  HarnessMeta,
  QuotaStatus,
  SetupCheck,
  UsageReport,
} from "~/protocol";

import type { AuthWires } from "./AuthFlowDialog";

import { Spinner } from "./ui/spinner";

const UsagePage = lazy(() => import("./Usage").then((m) => ({ default: m.UsagePage })));
const SetupPage = lazy(() => import("./Setup").then((m) => ({ default: m.SetupPage })));
const ToolsPage = lazy(() => import("./tools/ToolsPage").then((m) => ({ default: m.ToolsPage })));
const ThemePreview = lazy(() =>
  import("./ThemePreview").then((m) => ({ default: m.ThemePreview })),
);

// The screens that take over the whole viewport in place of the app.

function PageSpinner() {
  return (
    <div className="flex h-dvh items-center justify-center">
      <Spinner />
    </div>
  );
}

/**
 * The account-level Usage page, over the whole viewport.
 *
 * Historical usage: the server aggregates the event log and prices it, so
 * the phone only ever downloads the bucketed result.
 */
export function UsageScreen({
  clientRef,
  quotas,
  onClose,
}: {
  clientRef: RefObject<Client | null>;
  quotas: QuotaStatus[];
  onClose: () => void;
}) {
  const loadReport = useCallback(
    async (range: string): Promise<UsageReport> => {
      const res = await clientRef.current!.command("usage_report", { range });
      return res.report as UsageReport;
    },
    [clientRef],
  );
  const refreshQuota = useCallback(
    async (instance: string): Promise<QuotaStatus[]> => {
      const res = await clientRef.current!.command("quota_refresh", { instance });
      return (res.quotas ?? []) as QuotaStatus[];
    },
    [clientRef],
  );
  return (
    <Suspense fallback={<PageSpinner />}>
      <UsagePage
        quotas={quotas}
        onRefreshQuota={refreshQuota}
        loadReport={loadReport}
        onClose={onClose}
      />
    </Suspense>
  );
}

/**
 * The Skills page, with its MCP and Sign-ins tabs, over the whole viewport.
 * `scope` says whose skills it lists and which thread's session it asks.
 */
export function ToolsScreen({
  clientRef,
  scope,
  tab,
  onClose,
}: {
  clientRef: RefObject<Client | null>;
  scope: SkillsScope;
  tab?: ToolsTab;
  onClose: () => void;
}) {
  const command = useCallback(
    (name: string, args: unknown) => clientRef.current!.command(name, args),
    [clientRef],
  );
  const wires = useMemo<AuthWires>(
    () => ({
      command,
      subscribe: (flowId: string, listener: (ev: AuthFlowEvent) => void) =>
        clientRef.current?.onAuthFlow(flowId, listener) ?? (() => {}),
    }),
    [command, clientRef],
  );
  return (
    <Suspense fallback={<PageSpinner />}>
      <ToolsPage wires={wires} scope={scope} tab={tab} onClose={onClose} />
    </Suspense>
  );
}

/** The theme sample page at #themes. */
export function ThemePreviewScreen() {
  return (
    <Suspense fallback={<PageSpinner />}>
      <ThemePreview />
    </Suspense>
  );
}

/**
 * The first-run setup page at /setup. The desktop app opens it on first launch
 * and whenever the server reports setup incomplete; any browser can visit it.
 */
export function SetupScreen({
  harnesses,
  auth,
  onContinue,
}: {
  harnesses: HarnessMeta[];
  auth: ProviderAuth;
  onContinue: () => void;
}) {
  const { openInstanceAuth, recheck } = auth;
  // A check names a harness; sign-in goes to that harness's default account,
  // which is its first instance.
  const onLogin = useCallback(
    (check: SetupCheck) =>
      openInstanceAuth(harnesses.find((h) => h.id === check.id)?.instances[0]?.id ?? check.id),
    [harnesses, openInstanceAuth],
  );
  // Reload the report when the server's view of the harnesses changes (a
  // recheck after sign-in lands here) and when a sign-in dialog opens or
  // closes, so a finished sign-in shows as ready without a button press.
  const dialogOpen = !!(auth.loginInstance || auth.authInstance);
  const refreshKey = useMemo(() => ({ harnesses, dialogOpen }), [harnesses, dialogOpen]);
  return (
    <Suspense fallback={<PageSpinner />}>
      <SetupPage
        load={fetchSetup}
        onRecheck={recheck}
        onLogin={onLogin}
        onContinue={onContinue}
        refreshKey={refreshKey}
      />
    </Suspense>
  );
}
