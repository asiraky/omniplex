import { ArrowLeftIcon, Maximize2Icon, Undo2Icon } from "lucide-react";
import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { ErrorLine, LoadError, Loading, Segmented } from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Input } from "~/components/ui/input";
import { Marker } from "~/components/ui/marker";
import { Spinner } from "~/components/ui/spinner";
import { Textarea } from "~/components/ui/textarea";
import type { AgentApi } from "~/lib/agentApi";
import type { AgentFocus } from "~/lib/agentNav";
import { toast } from "~/lib/toast";
import { cn, errorText } from "~/lib/utils";
import type { Agent, AgentRevision, AgentTextKind } from "~/protocol";
import { useIsDesktop } from "~/useMediaQuery";
import { useLatest } from "~/useLatest";
import { compactApplies, compactUse, diffStat, fmtDay, revLabel } from "./agentText";
import { BodyText, DiffBox, WhoChip } from "./parts";
import { PreambleDialog } from "./shared";

type Draft = { text: string; base: number };
type Kept = { draft: Draft | null; reason: string };

/**
 * Unsaved edits by agent and kind, held by the agent page so a trip to another
 * tab and back finds them. Without one an edit lives as long as the editor.
 */
export const RevisionDrafts = createContext<Map<string, Kept> | null>(null);

/**
 * The Prompt and Compaction tabs: an editor on the latest revision, the
 * history beside it (under it on a phone), and any revision opened against any
 * other. Every save, a revert included, writes a new revision; history only
 * grows. `children` sits under the editor, for what one kind has and the
 * other lacks.
 */
export function RevisionsEditor({
  agent,
  api,
  kind,
  focus,
  children,
}: {
  agent: Agent;
  api: AgentApi;
  kind: AgentTextKind;
  focus?: AgentFocus;
  children?: ReactNode;
}) {
  const desktop = useIsDesktop();
  const isPrompt = kind === "prompt";
  const label = (n: number) => revLabel(kind, n);
  // The page may hand a fresh api object every render; the list is fetched
  // when the agent's revision number moves, not when that object does.
  const apiRef = useLatest(api);
  const headRev = isPrompt ? agent.promptRev : agent.compactRev;

  const [revs, setRevs] = useState<AgentRevision[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true;
    apiRef.current.revisions(agent.id, kind).then(
      (list) => {
        if (!live) return;
        setRevs([...list].sort((a, b) => a.n - b.n));
        setLoadError(null);
      },
      (e: unknown) => live && setLoadError(errorText(e)),
    );
    return () => {
      live = false;
    };
  }, [apiRef, agent.id, kind, headRev, attempt]);

  const current = revs?.[revs.length - 1];
  // An edit remembers the head it started from. Another revision landing
  // under it (an agent's approved edit, a save on another device) leaves the
  // text alone but says so, and the save names that base so the server can
  // refuse rather than drop the newer revision.
  const drafts = useContext(RevisionDrafts);
  const draftKey = `${agent.id}:${kind}`;
  const [draft, setDraft] = useState<Draft | null>(() => drafts?.get(draftKey)?.draft ?? null);
  // A draft typed back to the text it started from is no edit, whatever has landed since.
  const editing = draft && draft.text !== revs?.find((r) => r.n === draft.base)?.body ? draft : null;
  const edit = (text: string) => setDraft({ text, base: editing?.base ?? current?.n ?? headRev });
  const body = editing?.text ?? current?.body ?? "";
  const dirty = editing !== null && editing.text !== (current?.body ?? "");
  const stale = editing !== null && dirty && !!current && editing.base !== current.n;
  const [reason, setReason] = useState(() => drafts?.get(draftKey)?.reason ?? "");
  useEffect(() => {
    if (!drafts) return;
    if (draft || reason) drafts.set(draftKey, { draft, reason });
    else drafts.delete(draftKey);
  }, [drafts, draftKey, draft, reason]);
  const nextN = (current?.n ?? 0) + 1;

  const [selected, setSelected] = useState<number | null>(focus?.rev ?? null);
  const [focusRev, setFocusRev] = useState(focus?.rev);
  if (focusRev !== focus?.rev) {
    setFocusRev(focus?.rev);
    if (focus?.rev !== undefined) setSelected(focus.rev);
  }
  const opened = selected !== null ? revs?.find((r) => r.n === selected) : undefined;

  // On a phone the history sits under the editor, so opening a revision from
  // it brings the pane that now shows it back into view.
  const paneRef = useRef<HTMLDivElement>(null);
  const open = (n: number | null) => {
    setSelected(n);
    if (!desktop) paneRef.current?.scrollIntoView?.({ block: "start" });
  };

  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  // The revision a save from this tab left waiting on the turn, to name in
  // the banner; forgotten once the restart it waited for is done.
  const [pendingRev, setPendingRev] = useState<number | null>(null);
  const restartPending = !!agent.restartPending;
  const [sawPending, setSawPending] = useState(restartPending);
  if (sawPending !== restartPending) {
    setSawPending(restartPending);
    if (!restartPending) setPendingRev(null);
  }

  // A second Enter in the Reason field lands before the first save answers on
  // a slow link; it would send the same base and be refused as stale.
  const inFlight = useRef(false);
  /** The revision the save wrote, or null if it failed. */
  async function write(text: string, why: string, baseRev: number): Promise<number | null> {
    if (inFlight.current) return null;
    inFlight.current = true;
    setSaving(true);
    setSaveError(null);
    try {
      const res = await api.saveRevision(agent.id, kind, text, why, baseRev);
      setRevs((rs) => (rs && !rs.some((r) => r.n === res.revision.n) ? [...rs, res.revision] : rs));
      setPendingRev(res.restart === "pending" ? res.revision.n : null);
      const saved = label(res.revision.n);
      if (res.applyError) toast.error(`Saved ${saved}, but it did not reach the thread`, { description: res.applyError });
      else if (res.restart === "now") toast.success(`Saved ${saved}. The thread restarted on it.`);
      else if (res.restart === "pending") toast.success(`Saved ${saved}. The thread restarts on it when the current turn ends.`);
      else if (!isPrompt) toast.success(`Saved ${saved}. ${compactApplies(agent.harness, false)}`);
      else toast.success(agent.threadId ? `Saved ${saved}.` : `Saved ${saved}. It applies when a thread starts.`);
      return res.revision.n;
    } catch (e) {
      setSaveError(errorText(e));
      // A refusal as stale means a newer head this tab has not seen; fetch it
      // so the editor says so and offers to keep the edit over it.
      setAttempt((n) => n + 1);
      return null;
    } finally {
      inFlight.current = false;
      setSaving(false);
    }
  }

  async function save() {
    const sent = body;
    const why = reason;
    const n = await write(sent, why.trim(), editing?.base ?? current?.n ?? headRev);
    if (n === null) return;
    // Text typed while the save was out is kept, as an edit over what it wrote.
    const settle = (d: Draft | null) => (!d || d.text === sent ? null : { ...d, base: n });
    const settleReason = (r: string) => (r === why ? "" : r);
    setDraft(settle);
    setReason(settleReason);
    // The tab may have been left while the save was out; its kept edit settles too.
    const kept = drafts?.get(draftKey);
    if (kept) {
      const next = { draft: settle(kept.draft), reason: settleReason(kept.reason) };
      if (next.draft || next.reason) drafts!.set(draftKey, next);
      else drafts!.delete(draftKey);
    }
  }

  const [revertTarget, setRevertTarget] = useState<AgentRevision | null>(null);
  async function revert() {
    if (!revertTarget) return;
    if ((await write(revertTarget.body, `Revert to ${label(revertTarget.n)}`, current?.n ?? headRev)) !== null) {
      setRevertTarget(null);
      setSelected(null);
    }
  }

  // A restart waits while a turn runs; the agent says when one does. On the
  // Prompt tab it names the prompt coming in when the process is behind it
  // (this save, an agent's approved edit, or a save from another device).
  const live = agent.livePromptRev;
  const behind = isPrompt && live !== undefined && live < agent.promptRev;
  const waitingOn = behind ? agent.promptRev : pendingRev;
  // Saving restarts a live thread when the harness reads this text at start.
  const restarts = !!agent.threadId && (isPrompt || compactUse(agent.harness) === "start");

  const [showDiff, setShowDiff] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [preambleOpen, setPreambleOpen] = useState(false);

  const title = isPrompt ? "System prompt" : "Compaction instructions";
  const hint = !isPrompt
    ? `Saving writes a new revision. ${compactApplies(agent.harness, !!agent.threadId)}`
    : agent.threadId
      ? "Saving writes a new revision and restarts the thread. The transcript is kept, and the new prompt applies from the next message."
      : "Saving writes a new revision. It applies when a thread starts.";

  let pane: ReactNode;
  if (loadError && !revs) {
    pane = <LoadError message={loadError} onRetry={() => setAttempt((n) => n + 1)} />;
  } else if (!revs) {
    pane = <Loading>Loading revisions</Loading>;
  } else if (opened && current) {
    pane = (
      <RevisionView
        kind={kind}
        revs={revs}
        rev={opened}
        current={current}
        desktop={desktop}
        onBack={() => open(null)}
        onRevert={() => setRevertTarget(opened)}
      />
    );
  } else {
    pane = (
      <>
        {restartPending && (
          <div
            role="status"
            className="border-attention/40 bg-attention-surface text-attention-foreground mb-4 flex items-start gap-2 rounded-md border px-3 py-2 text-[12.5px] leading-snug"
          >
            <Spinner className="mt-0.5 size-3.5 shrink-0" aria-hidden />
            <span>
              {waitingOn !== null && <b>{label(waitingOn)} is saved. </b>}
              The thread restarts when the current turn ends.
              {behind && live !== undefined && ` Until then the agent still runs on ${label(live)}.`}
            </span>
          </div>
        )}
        <div className="mb-2 flex min-h-8 items-center gap-2">
          <h3 className="text-[13.5px] font-semibold">{title}</h3>
          {current && (
            <span className="text-muted-foreground rounded-full border px-1.5 font-mono text-[10.5px]">
              {label(current.n)}
            </span>
          )}
          <span className="flex-1" />
          {isPrompt && (
            <Button variant="ghost" size="sm" className="h-11 md:h-8" onClick={() => setPreambleOpen(true)}>
              Preamble
            </Button>
          )}
          <Button variant="ghost" size="sm" className="h-11 md:h-8" onClick={() => setExpanded(true)}>
            <Maximize2Icon className="size-3.5" />
            Expand
          </Button>
        </div>
        <Textarea
          aria-label={title}
          value={body}
          spellCheck={false}
          onChange={(e) => edit(e.target.value)}
          className="h-[55dvh] resize-y font-mono leading-relaxed [field-sizing:fixed] md:h-[30rem] md:text-[12.5px]"
        />
        {stale && current && (
          <div
            role="alert"
            className="border-attention/40 bg-attention-surface text-attention-foreground mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md border px-3 py-2 text-[12.5px] leading-snug"
          >
            <span className="min-w-0 flex-1 basis-60">
              <b>{label(current.n)} landed while you were editing.</b> Your text does not have its change.
            </span>
            <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={() => open(current.n)}>
              View {label(current.n)}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              className="h-11 md:h-8"
              onClick={() => {
                setDraft((d) => d && { ...d, base: current.n });
                setSaveError(null);
              }}
            >
              Keep my edit
            </Button>
          </div>
        )}
        {dirty ? (
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Input
              aria-label="Reason"
              placeholder="Reason (optional)"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.nativeEvent.isComposing) void save();
              }}
              className="min-w-0 basis-full md:basis-0 md:flex-1"
            />
            <Button variant="ghost" onClick={() => setDraft(null)} disabled={saving}>
              Discard
            </Button>
            <Button variant="outline" onClick={() => setShowDiff(true)}>
              Diff
            </Button>
            <Button onClick={() => void save()} disabled={saving} className="max-md:basis-full">
              {saving && <Spinner className="size-3.5" />}
              Save as {label(nextN)}
              {restarts ? " and restart" : ""}
            </Button>
          </div>
        ) : (
          <p className="text-muted-foreground mt-2 text-[12.5px] leading-snug">{hint}</p>
        )}
        {saveError && <ErrorLine message={saveError} className="mt-2" />}
        {children}
      </>
    );
  }

  return (
    <div className="mx-auto w-full max-w-6xl px-4 py-5 md:px-8 md:py-6">
      <div className="grid gap-8 md:grid-cols-[minmax(0,1fr)_17rem] lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div ref={paneRef} className="min-w-0 scroll-mt-4">
          {loadError && revs && (
            <LoadError
              message={`The history did not refresh, so it may be behind. ${loadError}`}
              onRetry={() => setAttempt((n) => n + 1)}
            />
          )}
          {pane}
        </div>
        {revs && (
          <RevisionList
            kind={kind}
            revs={revs}
            selected={opened?.n}
            onOpen={(n) => open(n)}
          />
        )}
      </div>

      {current && (
        <Dialog open={showDiff} onOpenChange={setShowDiff}>
          <DialogContent className="sm:max-w-3xl">
            <DialogHeader>
              <DialogTitle>
                {label(current.n)} → {label(nextN)}
              </DialogTitle>
              <DialogDescription>Unsaved changes against the current revision.</DialogDescription>
            </DialogHeader>
            <DiffBox
              from={label(current.n)}
              to={label(nextN)}
              before={current.body}
              after={body}
              className="scroll-thin max-h-[60dvh] overflow-y-auto"
            />
          </DialogContent>
        </Dialog>
      )}

      {current && revertTarget && (
        <Dialog open onOpenChange={(o) => !o && !saving && setRevertTarget(null)}>
          <DialogContent className="flex max-h-[90dvh] flex-col sm:max-w-2xl">
            <DialogHeader>
              <DialogTitle>Revert to {label(revertTarget.n)}?</DialogTitle>
              <DialogDescription>
                This writes {label(nextN)} with the text of {label(revertTarget.n)}.{" "}
                {!isPrompt
                  ? compactApplies(agent.harness, !!agent.threadId)
                  : agent.threadId
                    ? "The thread restarts on it, after the current turn if one is running."
                    : "It applies when a thread starts."}
              </DialogDescription>
            </DialogHeader>
            <DiffBox
              from={label(current.n)}
              to={label(nextN)}
              before={current.body}
              after={revertTarget.body}
              className="scroll-thin min-h-0 overflow-y-auto"
            />
            {saveError && <ErrorLine message={saveError} />}
            <DialogFooter>
              <Button variant="outline" onClick={() => setRevertTarget(null)} disabled={saving}>
                Cancel
              </Button>
              <Button onClick={() => void revert()} disabled={saving}>
                {saving && <Spinner className="size-3.5" />}
                Revert
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}

      <Dialog open={expanded} onOpenChange={setExpanded}>
        <DialogContent fullscreenOnMobile className="flex h-[90dvh] flex-col gap-3 sm:max-w-4xl">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
          </DialogHeader>
          <Textarea
            aria-label={`${title}, expanded`}
            value={body}
            spellCheck={false}
            onChange={(e) => edit(e.target.value)}
            className="min-h-0 flex-1 resize-none font-mono leading-relaxed [field-sizing:fixed] md:text-[12.5px]"
          />
          <DialogFooter>
            <Button onClick={() => setExpanded(false)}>Done</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {isPrompt && <PreambleDialog api={api} open={preambleOpen} onOpenChange={setPreambleOpen} />}
    </div>
  );
}

function RevisionList({
  kind,
  revs,
  selected,
  onOpen,
}: {
  kind: AgentTextKind;
  revs: AgentRevision[];
  selected?: number;
  onOpen: (n: number) => void;
}) {
  // Each revision's change against the one before it, worked out once per list.
  const stats = useMemo(
    () => new Map(revs.map((r, i) => [r.n, i > 0 ? diffStat(revs[i - 1].body, r.body) : null])),
    [revs],
  );
  const newest = revs[revs.length - 1]?.n;
  return (
    <section aria-label="History" className="min-w-0">
      <div className="mb-2 flex min-h-8 items-baseline gap-2 px-2">
        <h3 className="text-[13.5px] font-semibold">History</h3>
        <span className="text-muted-foreground text-[12px]">
          {revs.length} revision{revs.length === 1 ? "" : "s"}
        </span>
      </div>
      <ul className="space-y-0.5">
        {[...revs].reverse().map((r) => {
          const st = stats.get(r.n);
          return (
            <li key={r.n}>
              <button
                type="button"
                aria-current={selected === r.n || undefined}
                onClick={() => onOpen(r.n)}
                className={cn(
                  "hover:bg-accent/50 focus-visible:ring-ring flex min-h-11 w-full flex-col gap-0.5 rounded-md px-2.5 py-2 text-left outline-none focus-visible:ring-2",
                  selected === r.n && "bg-accent",
                )}
              >
                <span className="flex w-full items-center gap-2">
                  <b className="font-mono text-[12.5px]">{revLabel(kind, r.n)}</b>
                  <WhoChip author={r.author} />
                  <span className="text-muted-foreground text-[11.5px]">{fmtDay(r.createdAt)}</span>
                  {r.n === newest && <Marker tone="good">current</Marker>}
                  {st && (
                    <span className="ml-auto font-mono text-[11px] tabular-nums">
                      <span className="text-success">+{st.add}</span>{" "}
                      <span className="text-destructive">−{st.del}</span>
                    </span>
                  )}
                </span>
                {r.reason && (
                  <span className="line-clamp-2 text-[12.5px] leading-snug break-words">
                    {r.author === "agent" ? `“${r.reason}”` : r.reason}
                  </span>
                )}
              </button>
            </li>
          );
        })}
      </ul>
      <p className="text-muted-foreground mt-2 px-2.5 text-[12px] leading-snug">
        Revert writes a new revision with the old text. History only grows.
      </p>
    </section>
  );
}

/** One revision opened: why it was written, and how it differs from any other. */
function RevisionView({
  kind,
  revs,
  rev,
  current,
  desktop,
  onBack,
  onRevert,
}: {
  kind: AgentTextKind;
  revs: AgentRevision[];
  rev: AgentRevision;
  current: AgentRevision;
  desktop: boolean;
  onBack: () => void;
  onRevert: () => void;
}) {
  const label = (n: number) => revLabel(kind, n);
  // A pick stands for the revision it was made on; opening another goes back
  // to the default, the revision before it. 0: no comparison.
  const [cmp, setCmp] = useState<{ on: number; n: number } | null>(null);
  const otherN = cmp?.on === rev.n ? cmp.n : rev.n > 1 ? rev.n - 1 : 0;
  const other = otherN ? revs.find((r) => r.n === otherN) : undefined;
  const [older, newer] = other && other.n > rev.n ? [rev, other] : [other, rev];
  const [split, setSplit] = useState(true);
  // A phone has no room for two columns of prose, so it only ever gets unified.
  const layout = desktop && split ? "split" : "unified";

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <Button variant="ghost" size="sm" className="-ml-2 h-11 md:h-8" onClick={onBack}>
          <ArrowLeftIcon className="size-3.5" />
          Editor
        </Button>
        <h3 className="font-mono text-[13.5px] font-semibold">{label(rev.n)}</h3>
        <WhoChip author={rev.author} />
        <span className="text-muted-foreground text-[12px]">{fmtDay(rev.createdAt)}</span>
        <span className="flex-1" />
        {rev.n === current.n ? (
          <Marker tone="good">current</Marker>
        ) : rev.body === current.body ? (
          // Reverting would write the text that is already current.
          <Marker>same as {label(current.n)}</Marker>
        ) : (
          <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={onRevert}>
            <Undo2Icon className="size-3.5" />
            Revert to {label(rev.n)}
          </Button>
        )}
      </div>
      {rev.reason && (
        <div className="mb-4">
          <p className="text-muted-foreground text-[11.5px]">{rev.author === "agent" ? "You said" : "Reason"}</p>
          {rev.author === "agent" ? (
            <blockquote className="mt-1 border-l-2 pl-3 text-[13px] italic">“{rev.reason}”</blockquote>
          ) : (
            <p className="mt-1 text-[13px]">{rev.reason}</p>
          )}
        </div>
      )}
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <label className="text-muted-foreground flex items-center gap-2 text-[12px]">
          Compare with
          <select
            value={otherN}
            onChange={(e) => setCmp({ on: rev.n, n: Number(e.target.value) })}
            className="bg-background text-foreground h-11 min-w-36 rounded-md border px-2 text-base md:h-8 md:text-[13px]"
          >
            <option value={0}>Nothing, show full text</option>
            {revs
              .filter((r) => r.n !== rev.n)
              .reverse()
              .map((r) => (
                <option key={r.n} value={r.n}>
                  {label(r.n)}
                  {r.n === rev.n - 1 ? " (previous)" : r.n === current.n ? " (current)" : ""}
                </option>
              ))}
          </select>
        </label>
        <span className="flex-1" />
        {desktop && other && (
          <Segmented
            label="Diff layout"
            value={split ? "split" : "unified"}
            options={[
              { id: "split", label: "Split" },
              { id: "unified", label: "Unified" },
            ]}
            onChange={(v) => setSplit(v === "split")}
          />
        )}
      </div>
      {other && older && newer ? (
        <DiffBox
          from={label(older.n)}
          to={label(newer.n)}
          before={older.body}
          after={newer.body}
          layout={layout}
        />
      ) : (
        <BodyText>{rev.body}</BodyText>
      )}
    </div>
  );
}
