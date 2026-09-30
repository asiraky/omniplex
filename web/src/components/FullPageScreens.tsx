import { lazy, Suspense, useCallback, type RefObject } from "react";

import type { Client } from "~/client";
import type { Skill, SkillsScope } from "~/lib/skills";
import type { QuotaStatus, UsageReport } from "~/protocol";

import { Spinner } from "./ui/spinner";

const UsagePage = lazy(() => import("./Usage").then((m) => ({ default: m.UsagePage })));
const SkillsPage = lazy(() =>
  import("./skills/SkillsPage").then((m) => ({ default: m.SkillsPage })),
);
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
 * The Skills page, over the whole viewport. `scope` says whose skills it
 * lists; `onUse` is given only when there is a thread to put a skill into.
 */
export function SkillsScreen({
  clientRef,
  scope,
  onUse,
  onClose,
}: {
  clientRef: RefObject<Client | null>;
  scope: SkillsScope;
  onUse?: (skill: Skill) => void | Promise<void>;
  onClose: () => void;
}) {
  const command = useCallback(
    <T,>(name: string, args: Record<string, unknown>): Promise<T> =>
      clientRef.current!.command(name, args),
    [clientRef],
  );
  return (
    <Suspense fallback={<PageSpinner />}>
      <SkillsPage command={command} scope={scope} onUse={onUse} onClose={onClose} />
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
