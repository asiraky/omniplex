import {
  ArrowRightIcon,
  CheckIcon,
  CircleAlertIcon,
  CircleCheckIcon,
  CopyIcon,
  ExternalLinkIcon,
  KeyRoundIcon,
  LogInIcon,
  RefreshCwIcon,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { useCopy } from "~/lib/clipboard";
import { PROVIDER_LOGOS } from "~/lib/providerLogos";
import { checkStatus, joinOr, setupGaps, type CheckStatus } from "~/lib/setup";
import { cn } from "~/lib/utils";
import type { Remedy, SetupCheck, SetupReport } from "~/protocol";

import { Wordmark } from "./Logo";
import { ProviderLogo } from "./ProviderLogo";
import { Button } from "./ui/button";
import { Spinner } from "./ui/spinner";

// How long "Check again" waits for the server to re-probe the harnesses before
// asking for the report anyway. The socket queues a command while offline, so
// without a bound a dropped connection would leave the button spinning.
const RECHECK_WAIT_MS = 8000;

export interface SetupPageProps {
  /** Fetches the report; GET /api/setup in the app. */
  load: () => Promise<SetupReport>;
  /** Asks the server to look at the installed harnesses again. */
  onRecheck?: () => Promise<unknown> | void;
  /** Opens the app's sign-in flow for a check whose remedy is "login". */
  onLogin?: (check: SetupCheck) => void;
  onContinue: () => void;
  /** Changing this reloads the report, e.g. once a sign-in dialog closes. */
  refreshKey?: unknown;
}

/**
 * The first-run page at /setup: what this computer still needs before a
 * thread can start, and how to get it. Written for someone who has never
 * opened a terminal, so it says what is missing in plain words and puts the
 * fix one click away where it can.
 */
export function SetupPage({ load, onRecheck, onLogin, onContinue, refreshKey }: SetupPageProps) {
  const [report, setReport] = useState<SetupReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [checking, setChecking] = useState(true);
  // Only the latest request may land: a reload triggered by the harness list
  // changing can cross a "Check again" in flight.
  const seq = useRef(0);

  const refresh = useCallback(async () => {
    const n = ++seq.current;
    setChecking(true);
    try {
      const next = await load();
      if (n !== seq.current) return;
      setReport(next);
      setError(null);
    } catch (e) {
      if (n !== seq.current) return;
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      if (n === seq.current) setChecking(false);
    }
  }, [load]);

  useEffect(() => {
    void refresh();
  }, [refresh, refreshKey]);

  const checkAgain = async () => {
    setChecking(true);
    if (onRecheck) {
      // The report reads what the server last saw, so give it the chance to
      // look again first; something may have just been installed.
      const rechecked = Promise.resolve()
        .then(onRecheck)
        .catch(() => {});
      await Promise.race([rechecked, new Promise((r) => setTimeout(r, RECHECK_WAIT_MS))]);
    }
    await refresh();
  };

  const ready = report?.ready ?? false;

  return (
    <div className="bg-background fixed inset-0 z-50 flex flex-col">
      <div className="scroll-thin min-h-0 flex-1 overflow-y-auto px-4 pt-[calc(1.5rem+env(safe-area-inset-top))] pb-6 md:pt-12">
        <div className="mx-auto flex w-full max-w-xl flex-col gap-6">
          <header className="flex flex-col gap-3">
            <Wordmark className="h-7 self-start" />
            <h1 className="text-xl font-semibold tracking-tight">Set up this computer</h1>
            <p className="text-muted-foreground text-sm leading-relaxed">
              Omniplex runs AI coding assistants on this computer. It needs Git, and at least one
              assistant: Claude Code or Codex. You don't need both.
            </p>
          </header>

          {report ? (
            <SetupBody report={report} onLogin={onLogin} />
          ) : error ? null : (
            <div className="flex justify-center py-10">
              <Spinner className="text-muted-foreground size-5" />
            </div>
          )}

          {error && (
            <div
              role="alert"
              className="border-destructive/40 text-destructive rounded-lg border p-3 text-sm"
            >
              <p className="font-medium">Couldn't check this computer.</p>
              <p className="mt-1 text-[13px] opacity-90">{error}</p>
            </div>
          )}

          <p className="text-muted-foreground text-[13px] leading-relaxed">
            Installed something? Press <span className="font-medium">Check again</span>. If it
            still shows as missing, quit Omniplex and open it again so it notices the new install.
          </p>
        </div>
      </div>

      <footer className="bg-background border-t px-4 pt-3 pb-[calc(0.75rem+env(safe-area-inset-bottom))]">
        <div className="mx-auto flex w-full max-w-xl flex-col gap-2">
          {report && !ready && (
            <p className="text-muted-foreground text-[12px] leading-relaxed">
              You can look around, but threads won't start until the items above are sorted.
            </p>
          )}
          <div className="flex gap-2">
            <Button
              variant="outline"
              className="flex-1 md:flex-none"
              onClick={checkAgain}
              disabled={checking}
            >
              {checking ? <Spinner /> : <RefreshCwIcon />}
              Check again
            </Button>
            <Button
              variant={ready ? "default" : "outline"}
              className="flex-1 md:ml-auto md:flex-none"
              onClick={onContinue}
            >
              {ready ? "Open Omniplex" : "Continue anyway"}
              <ArrowRightIcon />
            </Button>
          </div>
        </div>
      </footer>
    </div>
  );
}

function SetupBody({
  report,
  onLogin,
}: {
  report: SetupReport;
  onLogin?: (check: SetupCheck) => void;
}) {
  const gaps = setupGaps(report);
  const tools = report.checks.filter((c) => c.kind === "tool");
  const harnesses = report.checks.filter((c) => c.kind === "harness");
  const needed = [
    ...gaps.tools.map((c) => c.name),
    ...(gaps.harnesses.length ? [joinOr(gaps.harnesses.map((c) => c.name))] : []),
  ];

  return (
    <>
      <div
        role="status"
        data-ready={report.ready}
        className={cn(
          "flex items-start gap-3 rounded-lg border p-3",
          report.ready
            ? "border-success/40 bg-success/10"
            : "border-attention/40 bg-attention-surface/60",
        )}
      >
        {report.ready ? (
          <CircleCheckIcon aria-hidden className="text-success mt-0.5 size-5 shrink-0" />
        ) : (
          <CircleAlertIcon aria-hidden className="text-attention mt-0.5 size-5 shrink-0" />
        )}
        <div className="text-sm leading-relaxed">
          {report.ready ? (
            <p className="font-medium">You're all set.</p>
          ) : (
            <>
              <p className="font-medium">A few things to sort out first.</p>
              {needed.length > 0 && (
                <p className="text-muted-foreground mt-0.5">Still needed: {needed.join("; ")}.</p>
              )}
            </>
          )}
        </div>
      </div>

      {tools.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="text-muted-foreground text-[12px] font-semibold tracking-wide uppercase">
            Required
          </h2>
          {tools.map((c) => (
            <CheckCard
              key={c.id}
              check={c}
              platformHint={platformHint(c, report.platform)}
              onLogin={onLogin}
            />
          ))}
        </section>
      )}

      {harnesses.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="text-muted-foreground text-[12px] font-semibold tracking-wide uppercase">
            AI assistant: you need at least one
          </h2>
          {harnesses.map((c) => (
            <CheckCard
              key={c.id}
              check={c}
              optionalBecause={
                gaps.readyHarness && gaps.readyHarness !== c ? gaps.readyHarness.name : undefined
              }
              onLogin={onLogin}
            />
          ))}
        </section>
      )}
    </>
  );
}

/**
 * Where the server's remedies say how, this says why, for the one tool whose
 * route differs by platform. Only shown while Git is missing.
 */
function platformHint(check: SetupCheck, platform: SetupReport["platform"]): string | undefined {
  if (check.id !== "git" || checkStatus(check) === "ready") return undefined;
  if (platform === "windows") return "On Windows this means Git for Windows. Claude Code needs it too.";
  if (platform === "darwin") return "On a Mac, Git comes with Apple's free Command Line Tools.";
  return undefined;
}

const STATUS_LABEL: Record<CheckStatus, string> = {
  ready: "Ready",
  "sign-in": "Needs sign-in",
  missing: "Not installed",
};

function StatusBadge({ status, muted }: { status: CheckStatus; muted?: boolean }) {
  const Icon =
    status === "ready" ? CircleCheckIcon : status === "sign-in" ? KeyRoundIcon : CircleAlertIcon;
  return (
    <span
      data-status={status}
      className={cn(
        "inline-flex shrink-0 items-center gap-1 rounded-full border px-2 py-0.5 text-[12px] font-medium whitespace-nowrap",
        muted
          ? "text-muted-foreground border-border"
          : status === "ready"
            ? "border-success/40 text-success"
            : status === "sign-in"
              ? "border-attention/40 text-attention-foreground bg-attention-surface/60"
              : "border-destructive/40 text-destructive",
      )}
    >
      <Icon aria-hidden className="size-3.5" />
      {STATUS_LABEL[status]}
    </span>
  );
}

function CheckCard({
  check,
  platformHint,
  optionalBecause,
  onLogin,
}: {
  check: SetupCheck;
  platformHint?: string;
  /** Another assistant is ready, so this one is not needed. */
  optionalBecause?: string;
  onLogin?: (check: SetupCheck) => void;
}) {
  const status = checkStatus(check);
  const { reason, remedy } = check.availability;
  return (
    <article
      aria-label={check.name}
      data-status={status}
      className={cn("bg-card rounded-lg border p-3 md:p-4", optionalBecause && "opacity-80")}
    >
      <div className="flex items-center gap-2">
        {PROVIDER_LOGOS[check.id] && <ProviderLogo provider={check.id} className="size-5" />}
        <h3 className="min-w-0 flex-1 truncate text-[15px] font-medium">{check.name}</h3>
        <StatusBadge status={status} muted={!!optionalBecause} />
      </div>

      {status !== "ready" && (
        <div className="mt-2 flex flex-col gap-2 text-sm">
          {optionalBecause && (
            <p className="text-muted-foreground text-[13px]">
              Optional: {optionalBecause} is already set up.
            </p>
          )}
          {reason && <p className="leading-relaxed">{reason}</p>}
          {platformHint && (
            <p className="text-muted-foreground text-[13px] leading-relaxed">{platformHint}</p>
          )}
          {remedy && remedy.length > 0 && (
            <ul className="flex flex-col gap-2">
              {remedy.map((r, i) => (
                <li key={i}>
                  <RemedyView check={check} remedy={r} onLogin={onLogin} />
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </article>
  );
}

function RemedyView({
  check,
  remedy,
  onLogin,
}: {
  check: SetupCheck;
  remedy: Remedy;
  onLogin?: (check: SetupCheck) => void;
}) {
  // Signing in is something the app can do itself, so it is a button rather
  // than a command to go and type.
  if (remedy.action === "login" && onLogin) {
    return (
      <Button onClick={() => onLogin(check)}>
        <LogInIcon />
        Sign in to {check.name}
      </Button>
    );
  }
  if (remedy.url) {
    return (
      <Button
        asChild
        variant="outline"
        className="h-auto min-h-11 justify-start py-2 text-left whitespace-normal md:min-h-9"
      >
        {/* A new tab in a browser; the desktop app sends these to the system browser. */}
        <a href={remedy.url} target="_blank" rel="noreferrer">
          <ExternalLinkIcon />
          {remedy.text}
        </a>
      </Button>
    );
  }
  return (
    <div className="flex flex-col gap-1.5">
      <p className="text-muted-foreground text-[13px] leading-relaxed">{remedy.text}</p>
      {remedy.command && <CommandLine command={remedy.command} />}
    </div>
  );
}

function CommandLine({ command }: { command: string }) {
  const { copied, copy } = useCopy();
  return (
    <div className="bg-muted flex items-center gap-1 rounded-md pl-3">
      <code className="min-w-0 flex-1 py-2 font-mono text-[12px] break-all">{command}</code>
      <Button
        variant="ghost"
        size="icon"
        aria-label={copied ? "Copied" : `Copy ${command}`}
        onClick={() => void copy(command)}
      >
        {copied ? <CheckIcon /> : <CopyIcon />}
      </Button>
    </div>
  );
}
