import { ArrowLeftIcon, Maximize2Icon, Undo2Icon } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";

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
import { diffStat, fmtDay, revLabel } from "./agentText";
import { BodyText, DiffBox, WhoChip } from "./parts";

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
  const [draft, setDraft] = useState<string | null>(null);
  const body = draft ?? current?.body ?? "";
  const dirty = draft !== null && draft !== (current?.body ?? "");
  const [reason, setReason] = useState("");
  const nextN = (current?.n ?? 0) + 1;

  const [selected, setSelected] = useState<number | null>(focus?.rev ?? null);
  useEffect(() => {
    if (focus?.rev !== undefined) setSelected(focus.rev);
  }, [focus?.rev]);
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
  const [pendingRev, setPendingRev] = useState<number | null>(null);

  async function write(text: string, why: string): Promise<boolean> {
    setSaving(true);
    setSaveError(null);
    try {
      const res = await api.saveRevision(agent.id, kind, text, why);
      setRevs((rs) => (rs && !rs.some((r) => r.n === res.revision.n) ? [...rs, res.revision] : rs));
      setPendingRev(res.restart === "pending" ? res.revision.n : null);
      const saved = label(res.revision.n);
      if (!isPrompt) toast.success(`Saved ${saved}. It applies at the next compaction.`);
      else if (res.restart === "now") toast.success(`Saved ${saved}. The thread restarted on it.`);
      else if (res.restart === "none") toast.success(`Saved ${saved}.`);
      return true;
    } catch (e) {
      setSaveError(errorText(e));
      return false;
    } finally {
      setSaving(false);
    }
  }

  async function save() {
    if (await write(body, reason.trim())) {
      setDraft(null);
      setReason("");
    }
  }

  const [revertTarget, setRevertTarget] = useState<AgentRevision | null>(null);
  async function revert() {
    if (!revertTarget) return;
    if (await write(revertTarget.body, `Revert to ${label(revertTarget.n)}`)) {
      setRevertTarget(null);
      setSelected(null);
    }
  }

  // A restart waits while a turn runs. Known from the save that asked for it,
  // or from the agent itself when its process is behind its newest prompt
  // (an agent's own approved edit, or a save from another device).
  const live = agent.livePromptRev;
  const waiting =
    isPrompt &&
    ((pendingRev !== null && !(live !== undefined && live >= pendingRev)) ||
      (!!agent.threadId && live !== undefined && live < agent.promptRev));
  const waitingOn = Math.max(pendingRev ?? 0, agent.promptRev);

  const [showDiff, setShowDiff] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [preambleOpen, setPreambleOpen] = useState(false);

  const title = isPrompt ? "System prompt" : "Compaction instructions";
  const hint = !isPrompt
    ? "Saving writes a new revision. It applies at the next compaction. No restart."
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
        {waiting && (
          <div
            role="status"
            className="border-attention/40 bg-attention-surface text-attention-foreground mb-4 flex items-start gap-2 rounded-md border px-3 py-2 text-[12.5px] leading-snug"
          >
            <Spinner className="mt-0.5 size-3.5 shrink-0" aria-hidden />
            <span>
              <b>{label(waitingOn)} is saved.</b> The thread restarts when the current turn ends.
              {live !== undefined && live < waitingOn && ` Until then the agent still runs on ${label(live)}.`}
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
          onChange={(e) => setDraft(e.target.value)}
          className="h-[55dvh] resize-y font-mono leading-relaxed [field-sizing:fixed] md:h-[30rem] md:text-[12.5px]"
        />
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
              {isPrompt && agent.threadId ? " and restart" : ""}
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
                  ? "It applies at the next compaction."
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
            onChange={(e) => setDraft(e.target.value)}
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
  // undefined: the default, the revision before this one. 0: no comparison.
  const [cmp, setCmp] = useState<number | undefined>(undefined);
  useEffect(() => setCmp(undefined), [rev.n]);
  const otherN = cmp ?? (rev.n > 1 ? rev.n - 1 : 0);
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
        {rev.n !== current.n ? (
          <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={onRevert}>
            <Undo2Icon className="size-3.5" />
            Revert to {label(rev.n)}
          </Button>
        ) : (
          <Marker tone="good">current</Marker>
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
            onChange={(e) => setCmp(Number(e.target.value))}
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

function PreambleDialog({
  api,
  open,
  onOpenChange,
}: {
  api: AgentApi;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [text, setText] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const apiRef = useLatest(api);
  useEffect(() => {
    if (!open || text !== null) return;
    let live = true;
    apiRef.current.preamble().then(
      (t) => live && setText(t),
      (e: unknown) => live && setError(errorText(e)),
    );
    return () => {
      live = false;
    };
  }, [open, text, apiRef]);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[90dvh] flex-col sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Agent preamble</DialogTitle>
          <DialogDescription>
            Fixed text written by Omniplex. It goes between the Omniplex instructions and your prompt.
          </DialogDescription>
        </DialogHeader>
        {error ? (
          <ErrorLine message={error} />
        ) : text === null ? (
          <Loading>Loading the preamble</Loading>
        ) : (
          <BodyText className="scroll-thin min-h-0 overflow-y-auto">{text}</BodyText>
        )}
      </DialogContent>
    </Dialog>
  );
}
