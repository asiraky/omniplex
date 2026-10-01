import { lazy, Suspense, useCallback, useMemo, type RefObject } from "react";

import type { Client } from "~/client";
import type { SkillsScope } from "~/lib/skills";
import type { ToolsTab } from "~/lib/toolsTab";
import type { AuthFlowEvent, QuotaStatus, UsageReport } from "~/protocol";

import type { AuthWires } from "./AuthFlowDialog";

import { Spinner } from "./ui/spinner";

const UsagePage = lazy(() => import("./Usage").then((m) => ({ default: m.UsagePage })));
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
