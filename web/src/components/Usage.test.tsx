// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";
import { UsagePage } from "./Usage";
import type { QuotaStatus, UsageReport } from "~/protocol";

const HOUR = 3600 * 1000;

function report(over: Partial<UsageReport> = {}): UsageReport {
  const from = Date.now() - 24 * HOUR;
  return {
    range: "24h",
    from,
    to: Date.now(),
    bucketMs: HOUR,
    priceVersion: "2026-04",
    rows: [
      {
        start: from + 2 * HOUR,
        provider: "claude",
        model: "claude-opus-5",
        totals: { input: 1_000_000, output: 500_000, cacheRead: 0, cacheWrite: 0, cost: 17.5, unpriced: 0 },
      },
      {
        start: from + 2 * HOUR,
        provider: "codex",
        model: "gpt-5.6-sol",
        totals: { input: 200_000, output: 100_000, cacheRead: 50_000, cacheWrite: 0, cost: 1.05, unpriced: 0 },
      },
    ],
    totals: { input: 1_200_000, output: 600_000, cacheRead: 50_000, cacheWrite: 0, cost: 18.55, unpriced: 0 },
    ...over,
  };
}

function quotaStatus(over: Partial<QuotaStatus> = {}): QuotaStatus {
  return {
    provider: "claude",
    instance: "claude",
    displayName: "Claude",
    snapshot: {
      checkedAt: Date.now() - 5 * 60 * 1000,
      plan: "max",
      windows: [
        { id: "five_hour", kind: "session", label: "Session", usedPercent: 42, resetsAt: Date.now() + 3 * HOUR },
        { id: "seven_day", kind: "weekly", label: "Weekly", usedPercent: 96, resetsAt: Date.now() + 3 * 24 * HOUR },
      ],
    },
    ...over,
  };
}

function renderPage(over: { report?: UsageReport; quotas?: QuotaStatus[] } = {}) {
  const loadReport = vi.fn().mockResolvedValue(over.report ?? report());
  const refresh = vi.fn().mockResolvedValue([]);
  render(
    <UsagePage
      quotas={over.quotas ?? [quotaStatus()]}
      onRefreshQuota={refresh}
      loadReport={loadReport}
      onClose={() => {}}
    />,
  );
  return { loadReport, refresh };
}

describe("UsagePage cost view", () => {
  it("labels the total as API-equivalent cost, not a bill", async () => {
    renderPage();

    await waitFor(() => expect(screen.getByText("$18.55")).toBeTruthy());
    expect(screen.getByText(/API-equivalent cost/)).toBeTruthy();
    // It must say what it is not, in plain words.
    expect(screen.getByText(/not your subscription bill/)).toBeTruthy();
  });

  it("carries the provider share and model breakdown from the same report", async () => {
    renderPage();

    await waitFor(() => expect(screen.getByText("$18.55")).toBeTruthy());
    expect(screen.getByText("claude")).toBeTruthy();
    expect(screen.getByText("codex")).toBeTruthy();
    expect(screen.getByText("claude-opus-5")).toBeTruthy();
    expect(screen.getByText("gpt-5.6-sol")).toBeTruthy();
  });

  it("identifies unpriced usage instead of hiding it", async () => {
    renderPage({
      report: report({
        totals: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, cost: 0, unpriced: 400_000 },
        rows: [],
      }),
    });

    await waitFor(() =>
      expect(screen.getByText(/no published price and are excluded from the total/)).toBeTruthy(),
    );
  });

  it("shows the exact bucket numbers on tap", async () => {
    renderPage();

    // The bar with usage is the third bucket; its aria-label carries the
    // bucket's exact cost.
    const bar = await waitFor(() =>
      screen.getByRole("button", { name: /\$18\.55/ }),
    );
    fireEvent.click(bar);

    // The per-row costs appear only in the tapped bucket's detail panel
    // (and again as the model's own total in the breakdown).
    expect(await screen.findAllByText("$17.50")).toBeTruthy();
    expect(screen.getAllByText("$1.05").length).toBeGreaterThan(0);
  });
});

describe("UsagePage limits view", () => {
  it("shows used and remaining percentages with a reset countdown, without hovering", async () => {
    renderPage({ quotas: [quotaStatus()] });
    fireEvent.click(screen.getByRole("tab", { name: "Limits" }));

    expect(await screen.findByText("42% used")).toBeTruthy();
    expect(screen.getByText(/58% left/)).toBeTruthy();
    expect(screen.getByText(/resets in 3h/)).toBeTruthy();
    expect(screen.getByText(/observed \d+m ago/)).toBeTruthy();
    expect(screen.getAllByRole("progressbar").length).toBe(2);
  });

  it("keeps one provider's figures when another's refresh fails", async () => {
    const healthy = quotaStatus();
    const failing = quotaStatus({
      provider: "codex",
      instance: "codex",
      displayName: "Codex",
      snapshot: { checkedAt: 0, windows: [] },
      lastError: "codex did not answer",
    });
    const { refresh } = renderPage({ quotas: [healthy, failing] });
    fireEvent.click(screen.getByRole("tab", { name: "Limits" }));

    // The failing provider says so — and the healthy one is untouched.
    expect(await screen.findAllByText(/The last refresh failed/)).toBeTruthy();
    expect(screen.getByText("42% used")).toBeTruthy();
    expect(refresh).not.toHaveBeenCalled();
  });

  it("refreshes one provider without touching the others", async () => {
    const { refresh } = renderPage({ quotas: [quotaStatus()] });
    fireEvent.click(screen.getByRole("tab", { name: "Limits" }));
    fireEvent.click(screen.getByRole("button", { name: /Refresh/ }));

    expect(refresh).toHaveBeenCalledWith("claude");
  });

  it("explains an account with no plan limits", async () => {
    renderPage({
      quotas: [
        quotaStatus({ snapshot: { checkedAt: Date.now(), unavailable: "unsupported" } }),
      ],
    });
    fireEvent.click(screen.getByRole("tab", { name: "Limits" }));

    expect(await screen.findByText(/no plan limits/)).toBeTruthy();
  });

  it("renders credits windows as a remaining count, not a percentage", async () => {
    renderPage({
      quotas: [
        quotaStatus({
          provider: "codex",
          instance: "codex",
          displayName: "Codex",
          snapshot: {
            checkedAt: Date.now(),
            windows: [{ id: "credits", kind: "credits", label: "Reset credits", count: 2 }],
          },
        }),
      ],
    });
    fireEvent.click(screen.getByRole("tab", { name: "Limits" }));

    expect(await screen.findByText("2 remaining")).toBeTruthy();
  });
});