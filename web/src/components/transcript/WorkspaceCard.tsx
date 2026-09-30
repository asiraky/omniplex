import { CheckIcon, TriangleAlertIcon, XIcon } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { Button } from "~/components/ui/button";
import { Spinner } from "~/components/ui/spinner";
import { cn } from "~/lib/utils";
import type { ThreadState } from "~/protocol";

// How long a ready workspace card stays before it collapses on its own: long
// enough to read "Workspace ready" and reach for the disclosure if the output
// is wanted, short enough that it is gone before the first prompt is typed.
const AUTO_DISMISS_MS = 2500;
// Matches the wrapper's transition duration, plus a frame's grace.
const COLLAPSE_MS = 320;

type Workspace = ThreadState["workspace"];

function workspaceTitle(state: ThreadState, failed: boolean): string {
  const ws = state.workspace;
  if (state.phase === "cleaning") return "Cleaning up workspace";
  if (failed) return "Workspace needs attention";
  if (ws.phase === "ready") return "Workspace ready";
  if (ws.phase === "released") return "Workspace released";
  return "Preparing workspace";
}

function elapsedLabel(ws: Workspace): string {
  return ws.durationMs ? `${Math.max(1, Math.round(ws.durationMs / 1000))}s` : "";
}

// The card's open/dismissed lifecycle, apart from what it draws.
function useWorkspaceCard(state: ThreadState) {
  const ws = state.workspace;
  const active =
    state.phase === "provisioning" || state.phase === "creating" || state.phase === "cleaning";
  const failed = state.phase === "provision_failed" || state.phase === "cleanup_failed";
  const [open, setOpen] = useState(active || failed);
  // A receipt is for the reader who watched the work. A workspace that was
  // already ready when this transcript mounted — every reopen of an old
  // thread — has nothing to report, so the card never appears: mounting it
  // only to auto-dismiss it 2.5s later would play its collapse above a
  // transcript pinned to the tail, and the tail wobbles as the scroller
  // chases the shrinking content a frame behind.
  const [dismissed, setDismissed] = useState(ws.phase === "ready" && !active && !failed);
  // The card's exit, in two steps: `leaving` starts the collapse, `dismissed`
  // unmounts it once the collapse has played. A hard unmount would make the
  // rest of the transcript jump.
  const [leaving, setLeaving] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);

  // The workspace moving on resets the card: going active again reopens it and
  // cancels any exit, and landing on ready folds it away. Adjusted during
  // render against the last phase seen, so the stale card never paints.
  const [seen, setSeen] = useState({ active, failed, phase: ws.phase });
  if (seen.active !== active || seen.failed !== failed || seen.phase !== ws.phase) {
    setSeen({ active, failed, phase: ws.phase });
    if (active || failed) {
      setOpen(true);
      setDismissed(false);
      setLeaving(false);
    } else if (ws.phase === "ready") setOpen(false);
  }

  // A finished provisioner is a receipt, not a task: it says its piece and
  // then gets out of the way, rather than holding the top of an empty
  // transcript until someone clicks the X. Only a *ready* workspace leaves —
  // a failed one is asking for a decision and must keep asking. So does an
  // expanded one: the reader opened it to look at the output, and yanking it
  // mid-read would be the same rudeness in the other direction.
  useEffect(() => {
    if (ws.phase !== "ready" || active || failed || open || dismissed) return;
    const timer = setTimeout(() => setLeaving(true), AUTO_DISMISS_MS);
    return () => clearTimeout(timer);
  }, [ws.phase, active, failed, open, dismissed]);

  // Collapse from the height it actually has: an animation from a guessed
  // max-height either clips the card or spends most of its duration playing
  // nothing. Two frames because the browser has to observe the start value
  // before the end value can transition from it.
  useEffect(() => {
    if (!leaving) return;
    const el = wrapRef.current;
    if (!el) {
      setDismissed(true);
      return;
    }
    el.style.maxHeight = `${el.scrollHeight}px`;
    let inner = 0;
    const outer = requestAnimationFrame(() => {
      inner = requestAnimationFrame(() => {
        el.style.maxHeight = "0px";
        el.style.opacity = "0";
      });
    });
    const timer = setTimeout(() => setDismissed(true), COLLAPSE_MS);
    return () => {
      cancelAnimationFrame(outer);
      cancelAnimationFrame(inner);
      clearTimeout(timer);
      // A workspace that goes active again mid-collapse cancels the exit, and
      // the card has to come back at full height — the inline styles the
      // collapse wrote would otherwise leave it flat and invisible, taking any
      // failure controls with it.
      el.style.maxHeight = "";
      el.style.opacity = "";
    };
  }, [leaving]);

  return {
    active,
    failed,
    open,
    toggle: () => setOpen((v) => !v),
    dismissed,
    leaving,
    leave: () => setLeaving(true),
    wrapRef,
  };
}

function StatusIcon({ active, failed }: { active: boolean; failed: boolean }) {
  if (active) return <Spinner className="text-primary size-4" />;
  if (failed) return <TriangleAlertIcon aria-hidden className="text-destructive size-4 shrink-0" />;
  return <CheckIcon aria-hidden className="text-success size-4 shrink-0" />;
}

type FailureHandlers = {
  onRetry: () => void;
  onCleanup: () => void;
  onForceDelete: () => void;
};

function FailureActions({
  phase,
  onRetry,
  onCleanup,
  onForceDelete,
}: FailureHandlers & { phase: ThreadState["phase"] }) {
  return (
    <div className="mt-3 flex flex-wrap gap-2">
      <Button size="sm" onClick={phase === "cleanup_failed" ? onCleanup : onRetry}>
        Retry
      </Button>
      {phase === "provision_failed" && (
        <Button size="sm" variant="outline" onClick={onCleanup}>
          Clean up
        </Button>
      )}
      {phase === "cleanup_failed" && (
        <Button size="sm" variant="destructive" onClick={onForceDelete}>
          Force delete…
        </Button>
      )}
    </div>
  );
}

function WorkspaceOutput({
  state,
  active,
  failed,
  ...handlers
}: FailureHandlers & { state: ThreadState; active: boolean; failed: boolean }) {
  const ws = state.workspace;
  return (
    <div className="border-t p-3">
      {ws.command && (
        <p className="text-muted-foreground mb-2 truncate font-mono text-[11px]">{ws.command}</p>
      )}
      <pre className="scroll-thin bg-muted/60 max-h-80 min-h-20 overflow-auto rounded-md p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap">
        {ws.output || (active ? "Starting…" : "No output")}
      </pre>
      {ws.error && (
        <p className="bg-destructive/10 text-destructive mt-2 rounded-md px-2 py-1.5 font-mono text-[11px]">
          {ws.error}
          {ws.exitCode ? ` (exit ${ws.exitCode})` : ""}
        </p>
      )}
      {failed && <FailureActions phase={state.phase} {...handlers} />}
    </div>
  );
}

export function WorkspaceCard({
  state,
  ...handlers
}: FailureHandlers & {
  state: ThreadState;
}) {
  const ws = state.workspace;
  const { active, failed, open, toggle, dismissed, leaving, leave, wrapRef } =
    useWorkspaceCard(state);

  if (!ws.phase || dismissed) return null;

  const elapsed = elapsedLabel(ws);

  return (
    <div
      ref={wrapRef}
      aria-hidden={leaving || undefined}
      className="motion-reduce:transition-none overflow-hidden transition-[max-height,opacity] duration-300 ease-in"
    >
      <div
        className={cn(
          "fade-in bg-card/70 rounded-xl border",
          failed && "border-destructive/40 bg-destructive/5",
        )}
      >
        <div className="flex items-center gap-2.5 px-3 py-2.5">
          <StatusIcon active={active} failed={failed} />
          <button
            type="button"
            onClick={toggle}
            aria-expanded={open}
            className="focus-visible:ring-ring min-h-11 min-w-0 flex-1 rounded-sm text-left outline-none focus-visible:ring-2 md:min-h-0"
          >
            <span className="text-muted-foreground block text-[11px]">Workspace provisioner</span>
            <span className="block text-[13px]">
              {workspaceTitle(state, failed)}
              {elapsed && ` · ${elapsed}`}
            </span>
          </button>
          {ws.phase === "ready" && (
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Dismiss workspace activity"
              className="size-11 md:size-8"
              onClick={leave}
            >
              <XIcon />
            </Button>
          )}
        </div>

        {open && <WorkspaceOutput state={state} active={active} failed={failed} {...handlers} />}
      </div>
    </div>
  );
}
