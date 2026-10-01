import type { SetupCheck, SetupReport } from "~/protocol";

/** Asks the server what this computer still needs. */
export async function fetchSetup(): Promise<SetupReport> {
  const res = await fetch("/api/setup", { cache: "no-store" });
  if (!res.ok) throw new Error(`The server answered ${res.status}.`);
  return (await res.json()) as SetupReport;
}

/** Where one check stands, in the words the page uses. */
export type CheckStatus = "ready" | "sign-in" | "missing";

export function checkStatus(check: SetupCheck): CheckStatus {
  const a = check.availability;
  if (a.state === "ready") return "ready";
  return a.remedy?.some((r) => r.action === "login") ? "sign-in" : "missing";
}

/** What still stands between this computer and a first thread. */
export interface SetupGaps {
  /** Required tools that are not ready. */
  tools: SetupCheck[];
  /** Every harness, when none is ready; empty once any one is. */
  harnesses: SetupCheck[];
  /** The harness that is ready, if one is: the others become optional. */
  readyHarness?: SetupCheck;
}

export function setupGaps(report: SetupReport): SetupGaps {
  const tools = report.checks.filter((c) => c.kind === "tool" && checkStatus(c) !== "ready");
  const harnesses = report.checks.filter((c) => c.kind === "harness");
  const readyHarness = harnesses.find((c) => checkStatus(c) === "ready");
  return { tools, harnesses: readyHarness ? [] : harnesses, readyHarness };
}

/** "A", "A or B", "A, B or C". */
export function joinOr(names: string[]): string {
  if (names.length <= 1) return names[0] ?? "";
  return `${names.slice(0, -1).join(", ")} or ${names[names.length - 1]}`;
}
